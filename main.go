package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"3dtest-server/protocol"
	"3dtest-server/serializer"

	"github.com/sirupsen/logrus"
	"github.com/topfreegames/pitaya/v2"
	"github.com/topfreegames/pitaya/v2/component"
	"github.com/topfreegames/pitaya/v2/config"
	"github.com/topfreegames/pitaya/v2/constants"
	"github.com/topfreegames/pitaya/v2/logger"
	logruswrapper "github.com/topfreegames/pitaya/v2/logger/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Room struct {
	component.Base
	app pitaya.Pitaya
}

func NewRoom(app pitaya.Pitaya) *Room {
	return &Room{
		app: app,
	}
}

func (r *Room) Init() {
	// Create the room group.
	// In a real app, you might create groups dynamically.
	err := r.app.GroupCreate(context.Background(), "room")
	if err != nil && err != constants.ErrGroupAlreadyExists {
		log.Printf("failed to create group: %s", err.Error())
	}
}

func (r *Room) Join(ctx context.Context, msg *protocol.JoinRequest) (*protocol.JoinResponse, error) {
	s := r.app.GetSessionFromCtx(ctx)
	// Bind session to a UID. Using session ID as UID for simplicity.
	uid := fmt.Sprintf("%d", s.ID())
	err := s.Bind(ctx, uid)
	if err != nil {
		// If already bound, we can ignore or handle it.
		// For now, assume it's a new session.
		if err != constants.ErrSessionAlreadyBound {
			return nil, err
		}
	}

	fmt.Printf("Client joined: %s (UID: %s)\n", msg.Name, uid)

	// Add to group
	err = r.app.GroupAddMember(ctx, "room", uid)
	if err != nil {
		log.Printf("failed to join group: %s", err.Error())
	}

	// Handle session close to remove from group
	s.OnClose(func() {
		r.app.GroupRemoveMember(context.Background(), "room", uid)
	})

	return &protocol.JoinResponse{
		Code:    200,
		Message: "Welcome to Pitaya Server (KCP)!",
	}, nil
}

func (r *Room) Message(ctx context.Context, msg *protocol.ChatMessage) {
	s := r.app.GetSessionFromCtx(ctx)
	fmt.Printf("Message from %s: %s\n", s.UID(), msg.Content)

	// Broadcast protobuf message
	// Route "OnMessage" must match client handler
	// frontendType should be the server type "game"
	err := r.app.GroupBroadcast(ctx, "game", "room", "OnMessage", &protocol.ChatMessage{
		SenderId: s.ID(),
		Content:  msg.Content,
	})
	if err != nil {
		log.Printf("Failed to broadcast message: %v", err)
	}
}

func main() {
	// Configure Logger
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{})
	l.SetLevel(logrus.DebugLevel)

	// Set up file logging
	fileLogger := &lumberjack.Logger{
		Filename:   "./logs/server.log",
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
	}

	// Multiwriter to write to both stdout and file
	mw := io.MultiWriter(os.Stdout, fileLogger)
	l.SetOutput(mw)

	logger.SetLogger(logruswrapper.NewWithFieldLogger(l))

	// Configure Pitaya
	// Standalone mode, no cluster
	builder := pitaya.NewDefaultBuilder(true, "game", pitaya.Standalone, map[string]string{}, *config.NewDefaultPitayaConfig())

	// Set Serializer to Custom Protobuf Serializer
	builder.Serializer = serializer.NewSerializer()

	// Add KCP Acceptor to Builder
	kcpAcc := NewKCPAcceptor(":3250")
	builder.AddAcceptor(kcpAcc)

	app := builder.Build()

	// Register Room Component
	app.Register(NewRoom(app),
		component.WithName("room"),
		component.WithNameFunc(strings.ToLower),
	)

	log.Println("Pitaya server starting on :3250 (KCP Mode)")
	app.Start()
}
