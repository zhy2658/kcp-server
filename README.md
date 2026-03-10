# Unity 3D 游戏服务器 (Pitaya + KCP + Protobuf)

这是一个专为 Unity 3D 多人游戏设计的高性能游戏服务器后端。它利用 **Pitaya** 作为微服务框架，**KCP** 提供可靠的 UDP 网络传输，以及 **Protobuf** 进行高效的数据序列化。

## 🏗 架构图

```ascii
+-----------------------------------------------------------------------+
|                       Unity Client (C#)                               |
|                                                                       |
|  +-------------+      +-------------+      +-----------------------+  |
|  | KCP Client  | <--> | Protobuf    | <--> | Pitaya Protocol Layer |  |
|  +-------------+      +-------------+      +-----------------------+  |
+---------^-------------------------------------------------------------+
          | UDP / KCP (Port 3250)
+---------v-------------------------------------------------------------+
|                       Game Server (Go)                                |
|                                                                       |
|  +-----------------+    +-----------------+    +-------------------+  |
|  | KCP Acceptor    |    |   Serializer    |    |   Viper Config    |  |
|  | (kcp_acceptor)  |    |  (Protobuf v2)  |    |   (config.yaml)   |  |
|  +--------+--------+    +--------+--------+    +---------+---------+  |
|           |                      |                       |            |
|           v                      v                       v            |
|  +-----------------------------------------------------------------+  |
|  |                 Pitaya Framework / Uber Fx (DI)                 |  |
|  |           (Session Management, Group Broadcasting)              |  |
|  +-------------------------------+---------------------------------+  |
|                                  |                                    |
|                                  v                                    |
|  +-----------------------------------------------------------------+  |
|  |                       Game Logic (Room)                         |  |
|  |                                                                 |
|  |  +------------+   +----------------+   +---------------------+  |  |
|  |  | Room Model |   |  Player Model  |   | Movement Sync       |  |  |
|  |  | (Mutex,    |   |  (Validation,  |   | (Force Correction)  |  |  |
|  |  |  State)    |   |   State)       |   |                     |  |  |
|  |  +------------+   +----------------+   +---------------------+  |  |
|  +-----------------------------------------------------------------+  |
|                                                                       |
+-----------------------------------------------------------------------+
```

## 🚀 核心功能

*   **可靠 UDP 网络 (KCP)**: 专为实时游戏优化的低延迟通信（支持 ARQ 自动重传请求、FEC 前向纠错）。
*   **高效序列化 (Protobuf)**: 强类型、紧凑的二进制消息格式。
*   **动态房间管理**: 支持创建、列表查询、加入和离开游戏房间。
*   **AOI (Area of Interest)**: 基于九宫格的高效视野管理系统，仅向附近玩家广播状态更新，显著降低网络带宽消耗。
*   **实时位置同步**: 优化的坐标同步与广播机制，包含**服务端移动校验**与**主动位置纠正**。
*   **依赖注入 (DI)**: 使用 **Uber Fx** 框架管理组件生命周期，代码结构清晰，易于测试。
*   **配置管理**: 支持通过 `config.yaml` 热加载配置 (使用 Viper)。
*   **可观测性**: 集成 Prometheus 指标，用于监控服务器性能。
*   **结构化日志**: 使用 Logrus + Lumberjack 实现日志轮转和文件输出。

## 🛠 技术栈

| 组件 | 技术 | 描述 |
| :--- | :--- | :--- |
| **语言** | Go (Golang) | 高并发，高性能 |
| **框架** | [Pitaya v2](https://github.com/topfreegames/pitaya) | 可扩展的游戏服务器框架 |
| **依赖注入** | [Uber Fx](https://github.com/uber-go/fx) | 模块化与依赖管理 |
| **传输层** | [KCP-Go](https://github.com/xtaci/kcp-go) | 可靠 UDP 库 |
| **序列化** | [Protobuf](https://developers.google.com/protocol-buffers) | Google 的数据交换格式 |
| **配置** | [Viper](https://github.com/spf13/viper) | 配置解决方案 |
| **日志** | Logrus | 结构化日志记录器 |

## 📡 协议与 API

所有消息均使用 Protocol Buffers 进行序列化。定义请参考 `protocol/game.proto`。

### 请求 / 响应 (客户端 -> 服务器 -> 客户端)

| 路由 (Route) | 请求 Proto | 响应 Proto | 描述 |
| :--- | :--- | :--- | :--- |
| `room.create` | `CreateRoomRequest` | `CreateRoomResponse` | 创建一个自定义名称/容量的新房间 |
| `room.list` | `ListRoomsRequest` | `ListRoomsResponse` | 获取可用房间列表及当前人数 |
| `room.join` | `JoinRequest` | `JoinResponse` | 加入指定房间 (默认: "lobby") |
| `room.leave` | `LeaveRequest` | - | 离开当前房间 |

### 通知 (Notify) (客户端 -> 服务器)

| 路由 (Route) | Proto | 描述 |
| :--- | :--- | :--- |
| `room.move` | `MoveRequest` | 发送位置/旋转/归一化速度/着地状态 (含速度校验) |
| `room.message`| `ChatMessage` | 发送聊天消息到房间 |

### 推送 / 广播 (服务器 -> 客户端)

| 路由 (Route) | Proto | 描述 |
| :--- | :--- | :--- |
| `OnPlayerJoin`| `PlayerJoinPush` | 通知房间内其他人有新玩家加入 |
| `OnPlayerLeave`| `PlayerLeavePush` | 通知有玩家离开 |
| `OnPlayerMove`| `PlayerMovePush` | 广播移动更新 (位置/旋转/速度/着地, AOI 过滤) |
| `OnPlayerEnterAOI` | `PlayerState` | **[新增]** 当有实体进入你的视野范围时触发（用于生成模型） |
| `OnPlayerLeaveAOI` | `PlayerLeavePush` | **[新增]** 当有实体离开你的视野范围时触发（用于销毁模型） |
| `OnMessage` | `ChatMessage` | 广播聊天消息 |
| `onForcePosition` | `ForcePositionPush` | **[新增]** 服务端强制纠正客户端位置（当移动非法时触发） |

## 🔌 Unity 客户端接入指南

### 前置条件
1.  **KCP 客户端库**: 你需要一个 C# 版的 KCP 客户端 (例如 `kcp-csharp` 或自行绑定的 `kcp` 库)。
2.  **Protobuf**: 使用 `protocol/game.proto` 生成 C# 类。
3.  **Pitaya 协议**: 实现 Pitaya 的握手流程和包结构 (Header + Body)。

### 连接步骤

1.  **连接 KCP**:
    *   目标 IP: `127.0.0.1` (本地) 或远程 IP
    *   端口: `3250` (可在 `config.yaml` 中配置)
    *   配置: NoDelay=1, Interval=10, Resend=2, NC=1

2.  **握手 (Handshake)**:
    *   发送 `Handshake` 包 (包含客户端信息的 JSON 负载)。
    *   等待 `Handshake` 响应。
    *   发送 `HandshakeAck`。

3.  **加入房间**:
    *   构造 `JoinRequest` Protobuf 消息。
    *   封装进 Pitaya Message (Type=`Request`, Route=`room.join`, ID=1)。
    *   编码并发送。
    *   等待具有匹配 ID 的 `Response`，获取 `JoinResponse` (包含 `room_id`)。

4.  **同步位置**:
    *   循环/Update: 使用当前 Transform 构造 `MoveRequest`。
    *   封装进 Pitaya Message (Type=`Notify`, Route=`room.move`)。
    *   发送 (即发即忘，无需等待响应)。
    *   **重要**: 监听 `onForcePosition` 推送。如果收到，必须立即将本地玩家位置重置为服务器下发的位置。

5.  **监听更新**:
    *   解码传入的包。
    *   `OnPlayerEnterAOI`: 在指定位置生成/显示玩家模型。
    *   `OnPlayerLeaveAOI`: 销毁/隐藏指定玩家模型。
    *   `OnPlayerMove`: 平滑插值更新目标玩家位置。

## ⚙️ 配置 (`config.yaml`)

```yaml
server:
  host: "0.0.0.0"
  port: 3250        # UDP 端口
  type: "game"      # 服务器类型
  debug: true

kcp:
  nodelay: 1
  interval: 10      # 内部更新间隔 (ms)
  resend: 2         # 快速重传次数
  nc: 1             # 关闭拥塞控制

game:
  heartbeat_interval: 30s
  max_room_players: 10

dashboard:
  enabled: true     # 是否启用控制台 UI (TUI)
```

## 🏃‍♂️ 运行服务器

### 编译
```powershell
go build -o server.exe
```

### 运行
```powershell
./server.exe
```

### 测试
使用内置的 Go 客户端验证连接性：
```powershell
go run cmd/client/main.go
```

### Bot2 拟人机器人

`cmd/bot2/bot2.go` 模拟真人玩家行为 (状态机: Idle→Walking/Running→Pausing 循环)，发送位置、旋转、速度数据，用于测试多人动画同步。

```powershell
# 默认参数
go run cmd/bot2/bot2.go

# 自定义
go run cmd/bot2/bot2.go -name "TestBot" -room "lobby" -addr "127.0.0.1:3250"
```

| 参数 | 值 |
|------|----|
| 漫游半径 | 15m |
| 行走/奔跑速度 | 2.5 / 5.5 u/s |
| 转向速度 | 180°/s |
| 网络发送 | 移动 10Hz, 静止 2Hz |

## Proto 代码生成

协议定义位于 `protocol/game.proto`，是 Go 服务器和 Unity 客户端共享的唯一协议源。

### 前置条件
- 安装 `protoc` (Protocol Buffers 编译器)
- 安装 Go 插件: `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`

### 生成 Go 代码
```bash
protoc --go_out=. --go_opt=paths=source_relative protocol/game.proto
```

### 生成 C# 代码 (Unity 客户端)
```bash
protoc --csharp_out=../3dtest/Assets/Scripts/Protocol protocol/game.proto
```

### 一键生成两端代码
```bash
protoc --go_out=. --go_opt=paths=source_relative \
       --csharp_out=../3dtest/Assets/Scripts/Protocol \
       protocol/game.proto
```

> **注意**: 修改 `.proto` 后务必重新生成两端代码，否则序列化/反序列化会不匹配。