package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
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
	name        = flag.String("name", "Bot", "Player Name")
	room        = flag.String("room", "lobby", "Room ID to join")
	create      = flag.Bool("create", false, "Create room before joining (deprecated, auto-detects)")
	role        = flag.String("role", "observer", "Role: observer or actor")
	reqID  uint = 1

	// Channels for state management
	joinSuccessChan  = make(chan bool, 1)
	createNeededChan = make(chan bool, 1)
	joinRetryChan    = make(chan bool, 1)

	// Channels for verification (Observer only)
	verifyJoinChan  = make(chan string, 10)
	verifyMoveChan  = make(chan string, 10)
	verifyLeaveChan = make(chan string, 10)

	// Map to track pending requests: ID -> RequestType ("join", "create")
	pendingRequests = make(map[uint]string)
)

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

	// 3. Logic
	// Always try to join first
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
	log.Println("Join sequence complete. Ready.")

	// 5. Role based behavior
	if *role == "actor" {
		runActorBehavior(conn)
	} else {
		runObserverBehavior()
	}
}

func runActorBehavior(conn *kcp.UDPSession) {
	log.Println("Starting Actor behavior...")

	// Wait a bit for observer to be ready
	time.Sleep(2 * time.Second)

	// 1. Move
	log.Println("Actor: Moving...")
	pos := &protocol.Vector3{X: 1, Y: 0, Z: 1}
	moveReq := &protocol.MoveRequest{
		Position: pos,
		Rotation: &protocol.Quaternion{W: 1},
	}
	d, _ := proto.Marshal(moveReq)
	sendNotify(conn, "room.move", d)
	time.Sleep(2 * time.Second)

	// 2. Chat
	log.Println("Actor: Chatting...")
	chatReq := &protocol.ChatMessage{Content: "Hello Observer"}
	d, _ = proto.Marshal(chatReq)
	sendNotify(conn, "room.message", d)
	time.Sleep(2 * time.Second)

	// 3. Leave
	log.Println("Actor: Leaving...")
	leaveReq := &protocol.LeaveRequest{}
	d, _ = proto.Marshal(leaveReq)
	sendRequest(conn, "room.leave", d)

	log.Println("Actor: Done. Exiting.")
	time.Sleep(1 * time.Second)
	os.Exit(0)
}

func runObserverBehavior() {
	log.Println("Starting Observer verification...")

	// Expect Join (Bot2)
	select {
	case name := <-verifyJoinChan:
		log.Printf("VERIFY: Passed - Received Join from %s", name)
	case <-time.After(10 * time.Second):
		log.Fatalf("VERIFY: Failed - Timeout waiting for Bot2 Join")
	}

	// Expect Move
	select {
	case id := <-verifyMoveChan:
		log.Printf("VERIFY: Passed - Received Move from %s", id)
	case <-time.After(5 * time.Second):
		log.Fatalf("VERIFY: Failed - Timeout waiting for Bot2 Move")
	}

	// Expect Leave
	select {
	case id := <-verifyLeaveChan:
		log.Printf("VERIFY: Passed - Received Leave from %s", id)
	case <-time.After(5 * time.Second):
		log.Fatalf("VERIFY: Failed - Timeout waiting for Bot2 Leave")
	}

	log.Println("ALL TESTS PASSED")
	os.Exit(0)
}

// ... Helpers ...
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
	// Track request type
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
				// Reply to heartbeat
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

		// Check for Pitaya Error (JSON format usually)
		// Try to decode as generic map to see if it has "code" and "msg"
		var pitayaErr struct {
			Code string `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(msg.Data, &pitayaErr) == nil && pitayaErr.Code != "" {
			log.Printf(">> Request %d (%s) Failed: %s - %s", msg.ID, route, pitayaErr.Code, pitayaErr.Msg)
			// Handle specific errors based on route
			if route == "room.join" {
				// If room not found (usually PIT-404 or custom code in msg)
				// Our server returns gameerror which might be wrapped or just string
				// The log showed "room not found" in msg.
				if pitayaErr.Code == "PIT-404" || pitayaErr.Code == "PIT-000" || strings.Contains(pitayaErr.Msg, "room not found") {
					log.Printf(">> Join Failed: Room Not Found. Requesting Create...")
					createNeededChan <- true
				} else {
					log.Printf(">> Join Failed with other error. Exiting.")
					os.Exit(1)
				}
			}
			return
		}

		if route == "room.create" {
			createResp := new(protocol.CreateRoomResponse)
			if err := proto.Unmarshal(msg.Data, createResp); err == nil && createResp.Id != "" {
				log.Printf(">> Room Created: %s (%s)", createResp.Name, createResp.Id)
				joinRetryChan <- true
			} else {
				log.Printf(">> Create Room Failed or Invalid Response")
			}
		} else if route == "room.join" {
			joinResp := new(protocol.JoinResponse)
			if err := proto.Unmarshal(msg.Data, joinResp); err == nil {
				if joinResp.Code == 200 {
					log.Printf(">> Joined Room: %s (%s)", joinResp.RoomId, joinResp.Message)
					joinSuccessChan <- true
				} else if joinResp.Code == 404 {
					log.Printf(">> Join Failed: Room Not Found (Proto). Requesting Create...")
					createNeededChan <- true
				} else if joinResp.Code == 0 && strings.Contains(joinResp.Message, "room not found") {
					// Handle case where error is returned as Pitaya Error (protobuf)
					// where field 2 (msg) matches JoinResponse.message, but field 1 (code) type mismatch
					log.Printf(">> Join Failed: Room Not Found (Pitaya Error). Requesting Create...")
					createNeededChan <- true
				} else {
					log.Printf(">> Join Failed: Code=%d Msg=%s", joinResp.Code, joinResp.Message)
					os.Exit(1)
				}
			}
		} else if route == "room.leave" {
			leaveResp := new(protocol.LeaveResponse)
			if err := proto.Unmarshal(msg.Data, leaveResp); err == nil {
				if leaveResp.Code == 200 {
					log.Printf(">> Left Room: %s", leaveResp.Message)
				} else {
					log.Printf(">> Leave Failed: Code=%d Msg=%s", leaveResp.Code, leaveResp.Message)
				}
			}
		}
	} else if msg.Type == message.Push {
		if msg.Route == "OnPlayerMove" {
			p := &protocol.PlayerMovePush{}
			proto.Unmarshal(msg.Data, p)
			log.Printf(">> [MOVE] %s -> (%.1f, %.1f)", p.Id, p.Position.X, p.Position.Z)
			// Send to verification channel for observer
			select {
			case verifyMoveChan <- p.Id:
			default:
			}
		} else if msg.Route == "OnPlayerJoin" {
			p := &protocol.PlayerJoinPush{}
			proto.Unmarshal(msg.Data, p)
			log.Printf(">> [JOIN] %s (%s)", p.Name, p.Id)
			// Send to verification channel for observer
			select {
			case verifyJoinChan <- p.Name:
			default:
			}
		} else if msg.Route == "OnPlayerLeave" {
			p := &protocol.PlayerLeavePush{}
			proto.Unmarshal(msg.Data, p)
			log.Printf(">> [LEAVE] %s", p.Id)
			// Send to verification channel for observer
			select {
			case verifyLeaveChan <- p.Id:
			default:
			}
		} else if msg.Route == "OnPlayerEnterAOI" {
			p := &protocol.PlayerState{}
			proto.Unmarshal(msg.Data, p)
			log.Printf(">> [AOI ENTER] %s at (%.1f, %.1f)", p.Id, p.Position.X, p.Position.Z)
		} else if msg.Route == "OnMessage" {
			p := &protocol.ChatMessage{}
			proto.Unmarshal(msg.Data, p)
			log.Printf(">> [CHAT] %s: %s", p.SenderId, p.Content)
		}
	}
}
