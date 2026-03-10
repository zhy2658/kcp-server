package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"game-server/protocol"

	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/conn/message"
	"github.com/topfreegames/pitaya/v2/conn/packet"
	"github.com/topfreegames/pitaya/v2/session"
	kcp "github.com/xtaci/kcp-go/v5"
	"google.golang.org/protobuf/proto"
)

var reqID uint = 1

func main() {
	log.SetPrefix("[MoveTest] ")

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
	log.Println("✓ Connected!")

	// 2. Start reading
	go readLoop(conn)

	// 3. Join room
	log.Println("→ Joining lobby...")
	joinReq := &protocol.JoinRequest{RoomId: "lobby", Name: "MoveTestBot"}
	data, _ := proto.Marshal(joinReq)
	sendRequest(conn, "room.join", data)

	time.Sleep(2 * time.Second)

	// 4. Try to move
	log.Println("→ Attempting to move to (10, 0, 10)...")
	moveReq := &protocol.MoveRequest{
		Position: &protocol.Vector3{X: 10, Y: 0, Z: 10},
		Rotation: &protocol.Quaternion{W: 1},
	}
	data, _ = proto.Marshal(moveReq)
	sendMove(conn, data) // ✅ Changed to use sendMove

	time.Sleep(2 * time.Second)

	// 5. Try another move
	log.Println("→ Attempting to move to (20, 0, 20)...")
	moveReq = &protocol.MoveRequest{
		Position: &protocol.Vector3{X: 20, Y: 0, Z: 20},
		Rotation: &protocol.Quaternion{W: 1},
	}
	data, _ = proto.Marshal(moveReq)
	sendMove(conn, data) // ✅ Changed to use sendMove

	time.Sleep(2 * time.Second)

	log.Println("✓ Test complete")
}

func handshake(conn *kcp.UDPSession) error {
	encoder := codec.NewPomeloPacketEncoder()
	decoder := codec.NewPomeloPacketDecoder()
	handshakeData := &session.HandshakeClientData{
		Platform:    "test",
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
	reqID++
	send(conn, msg)
}

func sendNotify(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{Type: message.Notify, Route: route, Data: data}
	send(conn, msg)
}

func sendMove(conn *kcp.UDPSession, data []byte) {
	// ✅ Move is now Request type
	sendRequest(conn, "room.move", data)
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
		if msg.Route == "room.join" {
			joinResp := new(protocol.JoinResponse)
			if err := proto.Unmarshal(msg.Data, joinResp); err == nil {
				if joinResp.Code == 200 {
					log.Printf("✓ Joined room: %s", joinResp.RoomId)
				} else {
					log.Printf("✗ Join failed: %s", joinResp.Message)
				}
			}
		} else if msg.Route == "room.move" {
			moveResp := new(protocol.MoveResponse)
			if err := proto.Unmarshal(msg.Data, moveResp); err == nil {
				if moveResp.Code == 200 {
					log.Printf("✓ Move SUCCESS: %s - Position confirmed: (%.1f, %.1f, %.1f)",
						moveResp.Message, moveResp.Position.X, moveResp.Position.Y, moveResp.Position.Z)
				} else {
					log.Printf("✗ Move FAILED: Code=%d Msg=%s", moveResp.Code, moveResp.Message)
				}
			}
		}
	} else if msg.Type == message.Push {
		if msg.Route == "OnPlayerMove" {
			p := &protocol.PlayerMovePush{}
			proto.Unmarshal(msg.Data, p)
			log.Printf("✓ [MOVE] %s -> (%.1f, %.1f, %.1f)", p.Id, p.Position.X, p.Position.Y, p.Position.Z)
		} else if msg.Route == "OnPlayerJoin" {
			p := &protocol.PlayerJoinPush{}
			proto.Unmarshal(msg.Data, p)
			log.Printf("✓ [JOIN] %s (%s)", p.Name, p.Id)
		} else if msg.Route == "onForcePosition" {
			p := &protocol.ForcePositionPush{}
			proto.Unmarshal(msg.Data, p)
			log.Printf("⚠ [FORCE_POS] Server corrected position to (%.1f, %.1f, %.1f)", p.Position.X, p.Position.Y, p.Position.Z)
		} else {
			log.Printf("← Push: %s", msg.Route)
		}
	}
}
