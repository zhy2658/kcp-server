package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"time"

	"game-server/protocol"

	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/conn/message"
	"github.com/topfreegames/pitaya/v2/conn/packet"
	"github.com/topfreegames/pitaya/v2/session"
	kcp "github.com/xtaci/kcp-go/v5"
	"google.golang.org/protobuf/proto"
)

func main() {
	// 1. Connect to KCP server
	// Disable FEC
	conn, err := kcp.DialWithOptions("127.0.0.1:3250", nil, 0, 0)
	if err != nil {
		log.Fatalf("Failed to dial KCP: %v", err)
	}
	defer conn.Close()

	// Configure KCP (must match server settings)
	conn.SetNoDelay(1, 10, 2, 1)
	conn.SetStreamMode(true)
	conn.SetWindowSize(128, 128)
	conn.SetACKNoDelay(true)

	fmt.Println("Connected to KCP server at 127.0.0.1:3250")

	// 2. Perform Handshake
	if err := handshake(conn); err != nil {
		log.Fatalf("Handshake failed: %v", err)
	}
	fmt.Println("Handshake successful!")

	// 3. Start reading loop
	go readLoop(conn)

	// 4. Create Room (Request)
	// Route: "room.create"
	roomName := fmt.Sprintf("Room-%d", rand.Intn(1000))
	createReq := &protocol.CreateRoomRequest{
		Name:       roomName,
		MaxPlayers: 10,
	}
	data, _ := proto.Marshal(createReq)
	sendRequest(conn, "room.create", data)

	// Wait for creation response (simulated by sleep, real client should wait callback)
	time.Sleep(500 * time.Millisecond)

	// 5. List Rooms (Request)
	// Route: "room.list"
	listReq := &protocol.ListRoomsRequest{}
	data, _ = proto.Marshal(listReq)
	sendRequest(conn, "room.list", data)

	time.Sleep(500 * time.Millisecond)

	// 6. Join Room (Request)
	// We'll try to join the room we created (assuming we know ID or just use "lobby" if logic changed)
	// For test simplicity, we join "lobby" first or wait for List response to parse ID.
	// But our client is dumb here. Let's just join "lobby" which is default created.
	joinReq := &protocol.JoinRequest{
		RoomId: "lobby",
		Name:   fmt.Sprintf("Player-%d", rand.Intn(1000)),
	}
	data, _ = proto.Marshal(joinReq)
	sendRequest(conn, "room.join", data)

	// 7. Start Moving
	go func() {
		ticker := time.NewTicker(1 * time.Second) // 1 Hz for test
		pos := &protocol.Vector3{X: 0, Y: 0, Z: 0}
		for range ticker.C {
			pos.X += 1.0
			pos.Z += 0.5

			moveReq := &protocol.MoveRequest{
				Position: pos,
				Rotation: &protocol.Quaternion{X: 0, Y: 0, Z: 0, W: 1},
			}
			data, _ := proto.Marshal(moveReq)
			sendNotify(conn, "room.move", data)
		}
	}()

	// Keep running
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}

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

var reqID uint = 1

func sendRequest(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{
		Type:  message.Request,
		ID:    reqID,
		Route: route,
		Data:  data,
	}
	reqID++
	send(conn, msg)
	fmt.Printf("Sent Request: %s (ID: %d)\n", route, msg.ID)
}

func sendNotify(conn *kcp.UDPSession, route string, data []byte) {
	msg := &message.Message{
		Type:  message.Notify,
		Route: route,
		Data:  data,
	}
	send(conn, msg)
	// Reduce spam
	if route != "room.move" {
		fmt.Printf("Sent Notify: %s\n", route)
	}
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

func readLoop(conn *kcp.UDPSession) {
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
				handleMessage(msg)
			} else if p.Type == packet.Heartbeat {
				// Reply to heartbeat if needed, but client usually just sends heartbeats?
				// Pitaya server expects heartbeats from client.
			}
		}
	}
}

func handleMessage(msg *message.Message) {
	if msg.Type == message.Response {
		fmt.Printf("Received Response (ID: %d)\n", msg.ID)

		// Simple routing based on ID guess (not robust)
		// Real implementation needs a callback map

		// Attempt to decode as various responses
		if joinResp := new(protocol.JoinResponse); proto.Unmarshal(msg.Data, joinResp) == nil && joinResp.Code != 0 {
			fmt.Printf("Join Response: Code=%d, Msg=%s, RoomID=%s\n", joinResp.Code, joinResp.Message, joinResp.RoomId)
		} else if createResp := new(protocol.CreateRoomResponse); proto.Unmarshal(msg.Data, createResp) == nil && createResp.Id != "" {
			fmt.Printf("Create Room Response: ID=%s, Name=%s\n", createResp.Id, createResp.Name)
		} else if listResp := new(protocol.ListRoomsResponse); proto.Unmarshal(msg.Data, listResp) == nil {
			fmt.Printf("List Rooms Response: %d rooms\n", len(listResp.Rooms))
			for _, r := range listResp.Rooms {
				fmt.Printf(" - %s (%s) %d/%d\n", r.Name, r.Id, r.Count, r.Max)
			}
		}

	} else if msg.Type == message.Push {
		// fmt.Printf("Received Push (Route: %s)\n", msg.Route)
		if msg.Route == "OnPlayerMove" {
			move := &protocol.PlayerMovePush{}
			proto.Unmarshal(msg.Data, move)
			fmt.Printf("Move: %s -> (%.1f, %.1f, %.1f)\n", move.Id, move.Position.X, move.Position.Y, move.Position.Z)
		} else if msg.Route == "OnPlayerJoin" {
			join := &protocol.PlayerJoinPush{}
			proto.Unmarshal(msg.Data, join)
			fmt.Printf("Player Joined: %s (%s)\n", join.Name, join.Id)
		} else if msg.Route == "OnPlayerLeave" {
			leave := &protocol.PlayerLeavePush{}
			proto.Unmarshal(msg.Data, leave)
			fmt.Printf("Player Left: %s\n", leave.Id)
		}
	}
}
