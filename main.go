package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"3dtest-server/internal/component"
	"3dtest-server/internal/config"
	"3dtest-server/internal/network"
	"3dtest-server/internal/serializer"

	"github.com/sirupsen/logrus"
	"github.com/topfreegames/pitaya/v2"
	pitayacomponent "github.com/topfreegames/pitaya/v2/component"
	pitayaconfig "github.com/topfreegames/pitaya/v2/config"
	"github.com/topfreegames/pitaya/v2/logger"
	logruswrapper "github.com/topfreegames/pitaya/v2/logger/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	// 1. Load Configuration
	if err := config.Load(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. Configure Logger
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{})
	level, err := logrus.ParseLevel(config.Conf.Log.Level)
	if err != nil {
		level = logrus.DebugLevel
	}
	l.SetLevel(level)

	// Set up file logging
	fileLogger := &lumberjack.Logger{
		Filename:   config.Conf.Log.Filename,
		MaxSize:    config.Conf.Log.MaxSize,
		MaxBackups: config.Conf.Log.MaxBackups,
		MaxAge:     config.Conf.Log.MaxAge,
	}

	// Multiwriter to write to both stdout and file
	mw := io.MultiWriter(os.Stdout, fileLogger)
	l.SetOutput(mw)

	logger.SetLogger(logruswrapper.NewWithFieldLogger(l))

	// 3. Configure Pitaya
	// Standalone mode, no cluster
	pitayaConfig := pitayaconfig.NewDefaultPitayaConfig()
	pitayaConfig.Heartbeat.Interval = config.Conf.Game.HeartbeatInterval

	// Metrics Configuration (Prometheus)
	pitayaConfig.Metrics.Prometheus.Enabled = true
	pitayaConfig.Metrics.Prometheus.Port = 9090

	builder := pitaya.NewDefaultBuilder(true, config.Conf.Server.Type, pitaya.Standalone, map[string]string{}, *pitayaConfig)

	// Set Serializer to Custom Protobuf Serializer
	builder.Serializer = serializer.NewSerializer()

	// Add KCP Acceptor to Builder
	addr := fmt.Sprintf("%s:%d", config.Conf.Server.Host, config.Conf.Server.Port)
	kcpAcc := network.NewKCPAcceptor(addr)
	builder.AddAcceptor(kcpAcc)

	app := builder.Build()

	// 4. Register Room Component
	app.Register(component.NewRoom(app),
		pitayacomponent.WithName("room"),
		pitayacomponent.WithNameFunc(strings.ToLower),
	)

	logger.Log.Infof("Pitaya server starting on %s (KCP Mode)", addr)
	app.Start()
}
