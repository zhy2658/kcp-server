# kcp-server

基于 **Pitaya**、**KCP** 和 **Protobuf** 构建的高性能 Unity 3D 多人游戏服务器后端。

## 项目概览

本项目实现了一个可靠的基于 UDP 的游戏服务器，专为实时多人交互设计。它利用 Pitaya 框架进行微服务架构管理，并使用 KCP 实现低延迟网络通信。

## 技术栈

- **语言**: Go (Golang)
- **框架**: [Pitaya v2](https://github.com/topfreegames/pitaya) (游戏服务器框架)
- **传输层**: [KCP](https://github.com/xtaci/kcp-go) (可靠 UDP)
- **序列化**: [Protobuf](https://developers.google.com/protocol-buffers) (Google Protocol Buffers)
- **依赖注入**: [Uber Fx](https://github.com/uber-go/fx)
- **配置**: [Viper](https://github.com/spf13/viper)
- **日志**: Logrus + Lumberjack
- **TUI 仪表盘**: Bubbletea

## 架构

服务器采用由 Uber Fx 管理的模块化架构。

### 核心组件

1.  **网络层 (Network Layer)**:
    - 使用自定义的 `KCPAcceptor` (`internal/network/kcp_acceptor.go`) 处理 KCP 连接。
    - 监听 UDP 端口 3250 (可配置)。
    - 通过配置 NoDelay、Interval、Resend 和 NC 优化低延迟性能。

2.  **应用层 (Application Layer)**:
    - **App Module** (`internal/app/app.go`): 使用 Fx 组装应用程序，设置日志、配置和 Pitaya。
    - **Room Component** (`internal/component/room.go`): 处理游戏逻辑，包括：
        - 房间的创建、列表查询、加入和离开。
        - 玩家移动同步。
        - AOI (Area of Interest, 感兴趣区域) 管理，用于带宽优化。
        - 聊天消息。

3.  **数据模型 (Data Models)**:
    - **Player** (`internal/models/player.go`): 存储玩家状态 (位置、旋转) 和验证逻辑。
    - **Room** (`internal/models/room.go`): 管理房间内的玩家及其 AOI 上下文。

4.  **协议 (Protocol)**:
    - 所有通信均使用 Protobuf (`protocol/game.proto`) 进行序列化。
    - 支持请求/响应 (Request/Response, 如: Join, Create) 和通知/推送 (Notify/Push, 如: Move, Chat) 模式。

## 项目结构

```
kcp-server/
├── cmd/                    # 入口点
│   ├── bot/                # 用于负载测试的机器人客户端
│   ├── client/             # 简单的测试客户端
│   ├── diagnostic_move/    # 诊断工具
│   └── test_move/          # 移动测试工具
├── internal/               # 私有应用代码
│   ├── aoi/                # AOI 逻辑 (基于网格)
│   ├── app/                # 应用程序组装 (Fx 模块)
│   ├── component/          # Pitaya 组件 (游戏逻辑)
│   ├── config/             # 配置加载
│   ├── dashboard/          # TUI 仪表盘实现
│   ├── gameerror/          # 自定义错误处理
│   ├── models/             # 领域模型 (Player, Room)
│   ├── network/            # 网络实现 (KCP)
│   └── serializer/         # 自定义序列化器 (Protobuf)
├── protocol/               # Protobuf 定义 (*.proto 和 *.pb.go)
├── config.yaml             # 服务器配置
├── go.mod                  # Go 模块定义
├── main.go                 # 服务器入口点
└── README.md               # 项目文档
```

## 协议与 API

定义在 `protocol/game.proto` 中。

### 请求 (客户端 -> 服务器)

| 路由 (Route) | 请求 (Request) | 响应 (Response) | 描述 |
| :--- | :--- | :--- | :--- |
| `room.create` | `CreateRoomRequest` | `CreateRoomResponse` | 创建新房间 |
| `room.list` | `ListRoomsRequest` | `ListRoomsResponse` | 列出可用房间 |
| `room.join` | `JoinRequest` | `JoinResponse` | 加入指定房间 |
| `room.leave` | `LeaveRequest` | `LeaveResponse` | 离开当前房间 |
| `room.move` | `MoveRequest` | `MoveResponse` | 同步玩家位置/旋转 |

### 通知 (客户端 -> 服务器)

| 路由 (Route) | 消息 (Message) | 描述 |
| :--- | :--- | :--- |
| `room.message` | `ChatMessage` | 发送聊天消息 |

### 推送消息 (服务器 -> 客户端)

| 路由 (Route) | 消息 (Message) | 描述 |
| :--- | :--- | :--- |
| `OnPlayerJoin` | `PlayerJoinPush` | 玩家加入了房间 |
| `OnPlayerLeave` | `PlayerLeavePush` | 玩家离开了房间 |
| `OnPlayerMove` | `PlayerMovePush` | 邻居移动了 (经 AOI 过滤) |
| `OnPlayerEnterAOI` | `PlayerState` | 玩家进入了本地视野 (AOI) |
| `OnPlayerLeaveAOI` | `PlayerLeavePush` | 玩家离开了本地视野 (AOI) |
| `OnMessage` | `ChatMessage` | 收到聊天消息 |
| `onForcePosition` | `ForcePositionPush` | 服务器纠正非法移动 |

## 配置

配置通过 `config.yaml` 管理：

```yaml
server:
  host: "0.0.0.0"
  port: 3250
  type: "game"

kcp:
  nodelay: 1
  interval: 10
  resend: 2
  nc: 1

game:
  heartbeat_interval: 30s
  max_room_players: 10
```

## 开发

### 编译与运行

```bash
# 编译
go build -o server.exe

# 运行
./server.exe
```

### 测试

- **单元测试**: 运行 `go test ./...`
- **客户端测试**: `go run cmd/client/main.go` 模拟客户端连接。

## 关键实现细节

- **AOI 系统**: 在 `internal/aoi` 中实现，可能使用基于网格的方法，通过仅向附近的玩家发送更新来限制网络流量。
- **并发**: `Room` 组件使用 `sync.RWMutex` 保护共享状态 (`rooms`, `players` 映射)。
- **仪表盘**: 提供了一个 TUI 仪表盘 (`internal/dashboard`) 用于实时服务器监控，可通过配置启用。
