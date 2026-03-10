// Game Client TUI
// Usage: go run cmd/client/main.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"strings"
	"sync"

	"game-server/protocol"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/conn/message"
	"github.com/topfreegames/pitaya/v2/conn/packet"
	"github.com/topfreegames/pitaya/v2/session"
	kcp "github.com/xtaci/kcp-go/v5"
	"google.golang.org/protobuf/proto"
)

var (
	conn          *kcp.UDPSession
	playerID      string
	roomID        string
	msgChan       chan *message.Message
	mu            sync.Mutex
	joinRetryChan = make(chan bool, 1)

	// Map to track pending requests: ID -> RequestType ("join", "create")
	pendingRequests = make(map[uint]string)
	pendingReqMu    sync.Mutex

	reqID   uint = 1
	reqIDMu sync.Mutex
)

// --- TUI Model ---

type model struct {
	viewport     viewport.Model
	messages     []string
	textInput    textinput.Model
	err          error
	connected    bool
	roomJoined   bool
	playerPos    *protocol.Vector3
	otherPlayers map[string]*protocol.Vector3 // ID -> Pos
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Type command (e.g., /join, /create, /move x z, /chat msg)..."
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 20

	vp := viewport.New(100, 20)
	vp.SetContent("Welcome to Game Client TUI!\nConnecting to server...")

	return model{
		viewport:     vp,
		messages:     []string{},
		textInput:    ti,
		connected:    false,
		roomJoined:   false,
		playerPos:    &protocol.Vector3{X: 0, Y: 0, Z: 0},
		otherPlayers: make(map[string]*protocol.Vector3),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, connectToServer)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textInput, tiCmd = m.textInput.Update(msg)

	// Only let viewport handle events if we are not typing a command or if specific keys are pressed
	// For example, PageUp/PageDown/Home/End always scroll viewport
	// Up/Down scroll viewport ONLY if text input is empty (optional, but good UX)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			m.viewport, vpCmd = m.viewport.Update(msg)
		default:
			// For other keys, viewport doesn't need to update unless we want scrolling
		}
	default:
		// For non-key messages (like content updates), always update viewport
		m.viewport, vpCmd = m.viewport.Update(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			val := m.textInput.Value()
			if val != "" {
				m.textInput.SetValue("")
				m.messages = append(m.messages, "> "+val)             // Echo input
				m.viewport.SetContent(strings.Join(m.messages, "\n")) // Explicitly update viewport content immediately
				m.viewport.GotoBottom()                               // Scroll to bottom
				return m, handleInput(val, &m)
			}
		}

	case errMsg:
		m.err = msg
		m.messages = append(m.messages, fmt.Sprintf("Error: %v", msg))
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
		return m, nil

	case serverMsg:
		m.messages = append(m.messages, string(msg))
		// Keep only last 100 messages
		if len(m.messages) > 100 {
			m.messages = m.messages[len(m.messages)-100:]
		}
		// Only update content
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
		m.viewport.GotoBottom()

		// Return batch to keep blinking and listening
		return m, tea.Batch(tiCmd, waitForServerMsg())

	case connectedMsg:
		m.connected = true
		m.messages = append(m.messages, "Connected to server!")
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
		return m, waitForServerMsg()

	case playerMoveMsg:
		// Update local state for visualization if needed
		// For now just logging is done in serverMsg
		if msg.id == playerID {
			m.playerPos = msg.pos
		} else {
			m.otherPlayers[msg.id] = msg.pos
		}
		return m, nil

	case playerLeaveMsg:
		delete(m.otherPlayers, msg.id)
		return m, nil

	case roomLeftMsg:
		// Clear all room-related state
		roomID = ""
		m.playerPos = &protocol.Vector3{X: 0, Y: 0, Z: 0}
		m.otherPlayers = make(map[string]*protocol.Vector3)
		m.messages = append(m.messages, msg.message)
		m.viewport.SetContent(strings.Join(m.messages, "\n"))
		m.viewport.GotoBottom()
		return m, nil

	case moveSuccessMsg:
		// Update local position after server confirms
		m.playerPos = msg.pos
		return m, nil
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m model) View() string {
	if !m.connected {
		return fmt.Sprintf("Connecting...\n\n%s", m.err)
	}

	// Status Bar
	status := fmt.Sprintf("Status: Connected | Room: %s | Pos: (%.1f, %.1f, %.1f)",
		roomID, m.playerPos.X, m.playerPos.Y, m.playerPos.Z)

	// AOI Visualization (Simple ASCII Map)
	mapView := renderMap(m.playerPos, m.otherPlayers)

	// Help Text
	help := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
		"Commands: /join [id], /create [name], /leave, /list, /move [x] [z], /chat [msg], /quit",
	)

	// Use lipgloss to join vertically
	return lipgloss.JoinVertical(lipgloss.Left,
		status,
		mapView,
		m.viewport.View(),
		help,
		m.textInput.View(),
	)
}

func renderMap(myPos *protocol.Vector3, others map[string]*protocol.Vector3) string {
	// Simple 20x10 grid centered on 0,0 (or player?)
	// Let's center on player
	width := 40
	height := 10
	grid := make([][]string, height)
	for y := 0; y < height; y++ {
		grid[y] = make([]string, width)
		for x := 0; x < width; x++ {
			grid[y][x] = "."
		}
	}

	// Helper to convert world pos to grid pos (relative to player)
	// Grid center is (width/2, height/2) -> Player Pos
	// Scale: 1 unit = 1 char? Maybe 2 units = 1 char for X

	toGrid := func(p *protocol.Vector3) (int, int, bool) {
		relX := p.X - myPos.X
		relZ := p.Z - myPos.Z

		// Scale: 1 char = 5 units
		gx := int(relX/5) + width/2
		gy := int(-relZ/5) + height/2 // Z up is Y down in grid (usually)

		if gx >= 0 && gx < width && gy >= 0 && gy < height {
			return gx, gy, true
		}
		return 0, 0, false
	}

	// Plot others
	for _, p := range others {
		gx, gy, ok := toGrid(p)
		if ok {
			grid[gy][gx] = "O"
		}
	}

	// Plot self
	grid[height/2][width/2] = "P"

	// Build string
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Render(
		func() string {
			var lines []string
			for _, row := range grid {
				lines = append(lines, strings.Join(row, ""))
			}
			return strings.Join(lines, "\n")
		}(),
	))
	return sb.String()
}

// --- Messages ---

type errMsg error
type serverMsg string
type connectedMsg struct{}
type playerMoveMsg struct {
	id  string
	pos *protocol.Vector3
}
type playerLeaveMsg struct {
	id string
}
type roomLeftMsg struct {
	message string
}
type moveSuccessMsg struct {
	pos *protocol.Vector3
}

// --- Commands ---

func connectToServer() tea.Msg {
	var err error
	// Disable FEC
	conn, err = kcp.DialWithOptions("127.0.0.1:3250", nil, 0, 0)
	if err != nil {
		return errMsg(err)
	}

	// Configure KCP (must match server settings)
	conn.SetNoDelay(1, 10, 2, 1)
	conn.SetStreamMode(true)
	conn.SetWindowSize(128, 128)
	conn.SetACKNoDelay(true)

	if err := handshake(conn); err != nil {
		return errMsg(err)
	}

	// Start reading loop in a goroutine that sends to msgChan
	msgChan = make(chan *message.Message, 100)
	go readLoop(conn, msgChan)

	return connectedMsg{}
}

func waitForServerMsg() tea.Cmd {
	return func() tea.Msg {
		msg := <-msgChan
		return handleServerMessage(msg)
	}
}

// var reqID uint = 1 // Moved to global block with mutex

func handleInput(input string, m *model) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	cmd := parts[0]

	switch cmd {
	case "/create":
		name := "Room-" + fmt.Sprintf("%d", rand.Intn(100))
		if len(parts) > 1 {
			name = parts[1]
		}
		req := &protocol.CreateRoomRequest{Name: name, MaxPlayers: 10}
		data, _ := proto.Marshal(req)
		sendRequest(conn, "room.create", data)
		return func() tea.Msg { return serverMsg("Creating room " + name + "...") }

	case "/join":
		rid := "lobby"
		if len(parts) > 1 {
			rid = parts[1]
		}
		req := &protocol.JoinRequest{RoomId: rid, Name: "User-" + fmt.Sprintf("%d", rand.Intn(100))}
		data, _ := proto.Marshal(req)
		sendRequest(conn, "room.join", data)
		// roomID = rid // Don't optimistic update, wait for success response
		return func() tea.Msg { return serverMsg("Joining room " + rid + "...") }

	case "/leave":
		req := &protocol.LeaveRequest{}
		data, _ := proto.Marshal(req)
		sendRequest(conn, "room.leave", data)
		// Don't clear state immediately - wait for server response
		return func() tea.Msg { return serverMsg("Leaving room...") }

	case "/move":
		if len(parts) < 3 {
			return func() tea.Msg { return serverMsg("Usage: /move x z") }
		}
		var x, z float32
		fmt.Sscanf(parts[1], "%f", &x)
		fmt.Sscanf(parts[2], "%f", &z)

		// Calculate target position
		targetPos := &protocol.Vector3{
			X: m.playerPos.X + x,
			Y: 0,
			Z: m.playerPos.Z + z,
		}

		req := &protocol.MoveRequest{
			Position: targetPos,
			Rotation: &protocol.Quaternion{W: 1},
		}
		data, _ := proto.Marshal(req)
		sendRequest(conn, "room.move", data) // ✅ Changed to Request
		return func() tea.Msg { return serverMsg(fmt.Sprintf("Moving to %.1f, %.1f...", targetPos.X, targetPos.Z)) }

	case "/list":
		req := &protocol.ListRoomsRequest{}
		data, _ := proto.Marshal(req)
		sendRequest(conn, "room.list", data)
		return func() tea.Msg { return serverMsg("Listing rooms...") }

	case "/chat":
		if len(parts) < 2 {
			return func() tea.Msg { return serverMsg("Usage: /chat message") }
		}
		content := strings.Join(parts[1:], " ")
		req := &protocol.ChatMessage{Content: content}
		data, _ := proto.Marshal(req)
		sendNotify(conn, "room.message", data)
		return nil

	default:
		return func() tea.Msg { return serverMsg("Unknown command: " + cmd) }
	}
}

// --- Helpers ---

func handleServerMessage(msg *message.Message) tea.Msg {
	if msg.Type == message.Response {
		pendingReqMu.Lock()
		route, ok := pendingRequests[msg.ID]
		if ok {
			delete(pendingRequests, msg.ID)
		}
		pendingReqMu.Unlock()

		// Helper to check for generic errors (JSON)
		var pitayaErr struct {
			Code interface{} `json:"code"` // Can be int or string
			Msg  string      `json:"msg"`
		}
		// We only try to parse error if Unmarshal to specific type fails, OR we check it first?
		// Pitaya error packets are usually valid JSON. Protobuf packets might not be valid JSON.
		// Let's try to unmarshal as specific type first based on route.

		if ok {
			switch route {
			case "room.join":
				joinResp := new(protocol.JoinResponse)
				if err := proto.Unmarshal(msg.Data, joinResp); err == nil && joinResp.Code != 0 {
					// Success or Protocol Error
					if joinResp.Code == 200 {
						playerID = "Me"
						roomID = joinResp.RoomId
						return serverMsg(fmt.Sprintf("Joined Room: %s (Msg: %s)", joinResp.RoomId, joinResp.Message))
					} else {
						return serverMsg(fmt.Sprintf("Join Failed: Code=%d Msg=%s", joinResp.Code, joinResp.Message))
					}
				}
			case "room.create":
				createResp := new(protocol.CreateRoomResponse)
				if err := proto.Unmarshal(msg.Data, createResp); err == nil && createResp.Id != "" {
					return serverMsg(fmt.Sprintf("Room Created: %s (ID: %s)", createResp.Name, createResp.Id))
				}
			case "room.list":
				listResp := new(protocol.ListRoomsResponse)
				if err := proto.Unmarshal(msg.Data, listResp); err == nil {
					s := "Rooms:\n"
					for _, r := range listResp.Rooms {
						s += fmt.Sprintf("- %s (%s) %d/%d\n", r.Name, r.Id, r.Count, r.Max)
					}
					return serverMsg(s)
				}
			case "room.leave":
				leaveResp := new(protocol.LeaveResponse)
				if err := proto.Unmarshal(msg.Data, leaveResp); err == nil && leaveResp.Code != 0 {
					if leaveResp.Code == 200 {
						// Return special message to clear state in Update handler
						return roomLeftMsg{message: fmt.Sprintf("Left Room: %s", leaveResp.Message)}
					} else {
						return serverMsg(fmt.Sprintf("Leave Failed: Code=%d Msg=%s", leaveResp.Code, leaveResp.Message))
					}
				}
			case "room.move":
				moveResp := new(protocol.MoveResponse)
				if err := proto.Unmarshal(msg.Data, moveResp); err == nil && moveResp.Code != 0 {
					if moveResp.Code == 200 {
						// Update position after server confirms
						return moveSuccessMsg{pos: moveResp.Position}
					} else {
						return serverMsg(fmt.Sprintf("Move Failed: Code=%d Msg=%s", moveResp.Code, moveResp.Message))
					}
				}
			}
		}

		// Fallback: Check if it's a generic error (if proto unmarshal failed or route unknown)
		if json.Unmarshal(msg.Data, &pitayaErr) == nil && pitayaErr.Msg != "" {
			return serverMsg(fmt.Sprintf("Error: %v - %s", pitayaErr.Code, pitayaErr.Msg))
		}

		return serverMsg(fmt.Sprintf("Received Response ID %d (Route: %s)", msg.ID, route))
	} else if msg.Type == message.Push {
		switch msg.Route {
		case "OnPlayerMove":
			p := &protocol.PlayerMovePush{}
			proto.Unmarshal(msg.Data, p)
			// Return special msg to update model state
			// We can't return complex state update here easily without tea.Msg
			// So we trigger a side effect? No, we return a Msg that Update handles.
			// But we also want to print log.
			// Let's return a wrapper msg.
			// Actually we need to return ONE msg.
			// We'll update the global store or make the msg carry data.
			return playerMoveMsg{id: p.Id, pos: p.Position}

		case "OnPlayerJoin":
			p := &protocol.PlayerJoinPush{}
			proto.Unmarshal(msg.Data, p)
			return serverMsg(fmt.Sprintf("Player Joined: %s (%s)", p.Name, p.Id))

		case "OnPlayerLeave":
			p := &protocol.PlayerLeavePush{}
			proto.Unmarshal(msg.Data, p)
			return playerLeaveMsg{id: p.Id} // Handle state update

		case "OnPlayerEnterAOI":
			p := &protocol.PlayerState{}
			proto.Unmarshal(msg.Data, p)
			// Update other players map
			// We need a way to pass this to Update.
			// Let's reuse playerMoveMsg for now since it has pos and id
			return playerMoveMsg{id: p.Id, pos: p.Position}

		case "OnPlayerLeaveAOI":
			p := &protocol.PlayerLeavePush{}
			proto.Unmarshal(msg.Data, p)
			return playerLeaveMsg{id: p.Id}

		case "OnMessage":
			p := &protocol.ChatMessage{}
			proto.Unmarshal(msg.Data, p)
			return serverMsg(fmt.Sprintf("[Chat] %s: %s", p.SenderId, p.Content))

		case "onForcePosition":
			p := &protocol.ForcePositionPush{}
			proto.Unmarshal(msg.Data, p)
			// We should force update local pos
			// Since we are "Me", we use special ID
			return playerMoveMsg{id: playerID, pos: p.Position}
		}
		return serverMsg(fmt.Sprintf("Push: %s", msg.Route))
	}
	return serverMsg("Unknown message type")
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// ... Handshake and Network utils (Keep existing logic but adapted) ...

func handshake(conn *kcp.UDPSession) error {
	encoder := codec.NewPomeloPacketEncoder()
	decoder := codec.NewPomeloPacketDecoder()

	// Send Handshake
	handshakeData := &session.HandshakeClientData{
		Platform:    "mac",
		LibVersion:  "0.3.5-release",
		BuildNumber: "20",
		Version:     "2.1",
	}
	data, _ := json.Marshal(map[string]interface{}{
		"sys":  handshakeData,
		"user": map[string]interface{}{},
	})

	pkt, err := encoder.Encode(packet.Handshake, data)
	if err != nil {
		return err
	}
	conn.Write(pkt)

	// Read Handshake Response
	header := make([]byte, codec.HeadLength)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	size, _, err := codec.ParseHeader(header)
	if err != nil {
		return err
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		return err
	}

	packets, err := decoder.Decode(append(header, body...))
	if err != nil {
		return err
	}

	if len(packets) == 0 || packets[0].Type != packet.Handshake {
		return fmt.Errorf("expected handshake packet")
	}

	// Send Handshake ACK
	ack, _ := encoder.Encode(packet.HandshakeAck, []byte{})
	conn.Write(ack)

	return nil
}

func sendRequest(conn *kcp.UDPSession, route string, data []byte) {
	reqIDMu.Lock()
	id := reqID
	reqID++
	reqIDMu.Unlock()

	pendingReqMu.Lock()
	pendingRequests[id] = route
	pendingReqMu.Unlock()

	msg := &message.Message{
		Type:  message.Request,
		ID:    id,
		Route: route,
		Data:  data,
	}
	send(conn, msg)
}

func sendNotify(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{
		Type:  message.Notify,
		Route: route,
		Data:  data,
	}
	send(conn, msg)
}

func send(conn *kcp.UDPSession, msg *message.Message) {
	msgEncoder := message.NewMessagesEncoder(false)
	encodedMsg, err := msgEncoder.Encode(msg)
	if err != nil {
		log.Printf("Failed to encode message: %v", err)
		return
	}

	pktEncoder := codec.NewPomeloPacketEncoder()
	packetData, err := pktEncoder.Encode(packet.Data, encodedMsg)
	if err != nil {
		log.Printf("Failed to encode packet: %v", err)
		return
	}

	conn.Write(packetData)
}

func readLoop(conn *kcp.UDPSession, ch chan<- *message.Message) {
	decoder := codec.NewPomeloPacketDecoder()
	header := make([]byte, codec.HeadLength)

	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			log.Printf("Read error: %v", err)
			return
		}

		size, _, err := codec.ParseHeader(header)
		if err != nil {
			log.Printf("Header parse error: %v", err)
			continue
		}

		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			log.Printf("Body read error: %v", err)
			return
		}

		packets, err := decoder.Decode(append(header, body...))
		if err != nil {
			log.Printf("Packet decode error: %v", err)
			continue
		}

		for _, p := range packets {
			if p.Type == packet.Data {
				msg, err := message.Decode(p.Data)
				if err != nil {
					log.Printf("Message decode error: %v", err)
					continue
				}
				ch <- msg
			} else if p.Type == packet.Heartbeat {
				// Reply to heartbeat
				// Pitaya server expects heartbeats from client.
				// For now we ignore, or we could implement a ticker to send heartbeats
				// Actually, we MUST reply to keep connection alive
				pktEncoder := codec.NewPomeloPacketEncoder()
				ack, _ := pktEncoder.Encode(packet.Heartbeat, nil)
				conn.Write(ack)
			}
		}
	}
}
