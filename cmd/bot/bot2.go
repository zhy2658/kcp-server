package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"game-server/protocol"

	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/conn/message"
	"github.com/topfreegames/pitaya/v2/conn/packet"
	"github.com/topfreegames/pitaya/v2/session"
	kcp "github.com/xtaci/kcp-go/v5"
	"google.golang.org/protobuf/proto"
)

var (
	name        = flag.String("name", "Bot2", "Player Name")
	room        = flag.String("room", "lobby", "Room ID to join")
	reqID  uint = 1

	// Channels for state management
	joinSuccessChan  = make(chan bool, 1)
	createNeededChan = make(chan bool, 1)
	joinRetryChan    = make(chan bool, 1)

	// Map to track pending requests: ID -> RequestType
	pendingRequests = make(map[uint]string)
)

// BotConfig defines the bot's behavior
type BotConfig struct {
	SpawnPos   protocol.Vector3
	MoveRadius float64
	MoveSpeed  float64
}

var config = BotConfig{
	SpawnPos:   protocol.Vector3{X: 76.5, Y: 10.2, Z: -54.0}, // Near the player spawn
	MoveRadius: 10.0,
	MoveSpeed:  3.0,
}

func main() {
	flag.Parse()
	log.SetPrefix(fmt.Sprintf("[%s] ", *name))

	// 1. Connect
	conn, err := kcp.DialWithOptions("127.0.0.1:3250", nil, 0, 0)
	if err != nil {
		log.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	conn.SetNoDelay(1, 10, 2, 1)
	conn.SetStreamMode(true)
	conn.SetWindowSize(128, 128)
	conn.SetACKNoDelay(true)

	if err := handshake(conn); err != nil {
		log.Fatalf("Handshake failed: %v", err)
	}
	log.Println("Connected!")

	// 2. Start Reading
	go readLoop(conn)

	// 3. Logic - Join Room
	log.Printf("Joining room %s...", *room)
	joinReq := &protocol.JoinRequest{RoomId: *room, Name: *name}
	data, _ := proto.Marshal(joinReq)
	sendRequest(conn, "room.join", data)

	// 4. Wait for Join Response
	joined := make(chan bool)
	go func() {
		for {
			select {
			case <-joinSuccessChan:
				joined <- true
				return
			case <-createNeededChan:
				log.Printf("Room not found. Creating room %s...", *room)
				req := &protocol.CreateRoomRequest{Name: *room, MaxPlayers: 10}
				d, _ := proto.Marshal(req)
				sendRequest(conn, "room.create", d)
			case <-joinRetryChan:
				log.Printf("Retrying join room %s...", *room)
				d, _ := proto.Marshal(joinReq)
				sendRequest(conn, "room.join", d)
			}
		}
	}()

	<-joined
	log.Println("Join sequence complete. Starting AI behavior.")

	// 5. Run Bot AI
	runBotBehavior(conn)
}

func runBotBehavior(conn *kcp.UDPSession) {
	ticker := time.NewTicker(100 * time.Millisecond) // Update every 100ms
	defer ticker.Stop()

	// Initial Position
	currentPos := config.SpawnPos
	targetPos := getRandomTarget()
	
	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			// Simple AI: Move towards target
			dist := distance(currentPos, targetPos)
			if dist < 0.5 {
				// Reached target, pick new one
				targetPos = getRandomTarget()
				// log.Printf("Reached target, new target: (%.1f, %.1f)", targetPos.X, targetPos.Z)
			} else {
				// Move towards target
				dx := float64(targetPos.X - currentPos.X)
				dz := float64(targetPos.Z - currentPos.Z)
				
				// Normalize
				invDist := 1.0 / dist
				dx *= invDist
				dz *= invDist
				
				// Apply speed (units per second * delta time)
				step := config.MoveSpeed * 0.1 
				
				currentPos.X += float32(dx * step)
				currentPos.Z += float32(dz * step)
				
				// Calculate rotation (Y-axis)
				// angle := math.Atan2(dz, dx) * 180 / math.Pi
				// Convert to Quaternion (Y-axis rotation)
				// Euler(0, -angle + 90, 0) roughly
				// For simplicity, we just send Identity or basic rotation
				// Unity uses (x,y,z,w). Quaternion.Euler(0, angle, 0)
				
				// Send Move Update
				moveReq := &protocol.MoveRequest{
					Position: &currentPos,
					Rotation: &protocol.Quaternion{Y: 0, W: 1}, // Simplified rotation
				}
				d, _ := proto.Marshal(moveReq)
				sendNotify(conn, "room.move", d)
			}
		case <-c:
			log.Println("Bot stopping...")
			return
		}
	}
}

func getRandomTarget() protocol.Vector3 {
	angle := rand.Float64() * 2 * math.Pi
	r := rand.Float64() * config.MoveRadius
	
	return protocol.Vector3{
		X: config.SpawnPos.X + float32(r * math.Cos(angle)),
		Y: config.SpawnPos.Y,
		Z: config.SpawnPos.Z + float32(r * math.Sin(angle)),
	}
}

func distance(a, b protocol.Vector3) float64 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	dz := float64(a.Z - b.Z)
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// ... Network Helpers (Same as main.go) ...

func handshake(conn *kcp.UDPSession) error {
	encoder := codec.NewPomeloPacketEncoder()
	decoder := codec.NewPomeloPacketDecoder()
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
	ack, _ := encoder.Encode(packet.HandshakeAck, []byte{})
	conn.Write(ack)
	return nil
}

func sendRequest(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{Type: message.Request, ID: reqID, Route: route, Data: data}
	pendingRequests[reqID] = route
	reqID++
	send(conn, msg)
}

func sendNotify(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{Type: message.Notify, Route: route, Data: data}
	send(conn, msg)
}

func send(conn *kcp.UDPSession, msg *message.Message) {
	msgEncoder := message.NewMessagesEncoder(false)
	encodedMsg, err := msgEncoder.Encode(msg)
	if err != nil {
		log.Printf("Encode msg error: %v", err)
		return
	}
	pktEncoder := codec.NewPomeloPacketEncoder()
	packetData, err := pktEncoder.Encode(packet.Data, encodedMsg)
	if err != nil {
		log.Printf("Encode pkt error: %v", err)
		return
	}
	conn.Write(packetData)
}

func readLoop(conn *kcp.UDPSession) {
	decoder := codec.NewPomeloPacketDecoder()
	header := make([]byte, codec.HeadLength)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		size, _, err := codec.ParseHeader(header)
		if err != nil {
			continue
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		packets, err := decoder.Decode(append(header, body...))
		if err != nil {
			continue
		}
		for _, p := range packets {
			if p.Type == packet.Data {
				msg, err := message.Decode(p.Data)
				if err != nil {
					continue
				}
				handleMessage(msg)
			} else if p.Type == packet.Heartbeat {
				pktEncoder := codec.NewPomeloPacketEncoder()
				ack, _ := pktEncoder.Encode(packet.Heartbeat, nil)
				conn.Write(ack)
			}
		}
	}
}

func handleMessage(msg *message.Message) {
	if msg.Type == message.Response {
		route, ok := pendingRequests[msg.ID]
		if ok {
			delete(pendingRequests, msg.ID)
		}

		// Simple error check
		var pitayaErr struct {
			Code string `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(msg.Data, &pitayaErr) == nil && pitayaErr.Code != "" {
			if route == "room.join" {
				if pitayaErr.Code == "PIT-404" || strings.Contains(pitayaErr.Msg, "room not found") {
					createNeededChan <- true
				} else {
					os.Exit(1)
				}
			}
			return
		}

		if route == "room.create" {
			createResp := new(protocol.CreateRoomResponse)
			if err := proto.Unmarshal(msg.Data, createResp); err == nil && createResp.Id != "" {
				log.Printf(">> Room Created: %s", createResp.Name)
				joinRetryChan <- true
			}
		} else if route == "room.join" {
			joinResp := new(protocol.JoinResponse)
			if err := proto.Unmarshal(msg.Data, joinResp); err == nil {
				if joinResp.Code == 200 {
					log.Printf(">> Joined Room: %s", joinResp.RoomId)
					joinSuccessChan <- true
				} else if joinResp.Code == 404 || strings.Contains(joinResp.Message, "room not found") {
					createNeededChan <- true
				}
			}
		}
	}
}
