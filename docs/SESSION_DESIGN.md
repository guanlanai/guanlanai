# 会话模型技术设计

> 目标：私聊支持多会话管理；群组按“每个成员一条会话”隔离。
> 状态：Phase A/B 已实现并部署到 prod/staging（迁移 `0002_sessions_scope.sql`、会话仓储、私聊多会话命令和群组按成员隔离）。两套环境均已设置 `GROUPS_ENABLED=true`。

## 1. 目标与非目标

### 目标

- 私聊：一个用户在一个私聊 chat 中可以有多个会话，可新建、查看、切换、重命名、删除。
- 群组：一个用户在同一个群/超级群里恰好一条会话，不需要也不能切换。
- 群 A 的成员 1 和成员 2 各自有独立会话，互不串上下文。
- Key、模型、模式仍然按用户全局配置，不按会话复制。
- `/reset` 只清空当前会话；不取消、不丢弃在途分析。
- 分析过程中切换私聊会话或 `/reset`，结果仍写回**发起分析时捕获的 session**。
- 现有私聊数据可平滑迁移，不丢消息和用量。

### 非目标

- 群组共享一条会话。
- 群管理员共享 Key、群级额度或群级计费。
- 跨群/跨私聊共享上下文。
- Web 端会话管理、导出、搜索。
- 群组管理员审批/白名单体系（后续可加）。

## 2. 会话语义

```text
私聊：user_id + chat_id -> N 条 session，其中一条 active
群组：user_id + chat_id -> 1 条 session
```

- 私聊 chat 的 `chat_id` 通常等于用户 ID，但不依赖这个假设。
- 群组 chat 的 `chat_id` 是群 ID；不同成员用 `user_id` 区分。
- `scope` 只允许 `private` / `group`。
- `title` 用于私聊列表展示；群组也保存标题，但只显示当前一条。
- `current_symbol`、`summary`、`last_analysis_json`、`generation` 仍然属于单个 session。
- `usage_events`、限流、Key、模型、模式仍按用户统计，不按会话复制。

## 3. 数据模型

### 3.1 新 sessions 表

```sql
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id INTEGER NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('private', 'group')),
    title TEXT,
    current_symbol TEXT,
    current_market TEXT,
    summary TEXT,
    summary_tokens INTEGER NOT NULL DEFAULT 0,
    last_analysis_json TEXT,
    generation INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_sessions_user_chat ON sessions(user_id, chat_id, scope);
CREATE UNIQUE INDEX ux_sessions_group ON sessions(chat_id, user_id)
    WHERE scope = 'group';
```

- 去掉当前的 `user_id UNIQUE` 和 `chat_id UNIQUE`。
- 群组用 partial unique index 保证一个用户在一个群只有一条。
- 私聊允许多条，不加唯一索引。

### 3.2 私聊 active 指针

```sql
CREATE TABLE chat_state (
    chat_id INTEGER PRIMARY KEY,
    active_session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

只为私聊使用；群组通过 `(chat_id, user_id, scope='group')` 唯一查找，不需要 active 指针。

### 3.3 messages

结构不变：

```sql
messages(id, session_id, role, content, tokens, created_at)
```

仍然通过 `session_id` 关联；历史消息按 session 隔离。

### 3.4 迁移 0002

现有 `sessions` 的唯一约束无法直接 `ALTER TABLE` 删除，需要重建表：

1. 创建 `sessions_new`，包含 `scope`、`title`、`created_at`。
2. 复制旧数据：
   - `scope='private'`
   - `title=COALESCE(current_symbol, '默认会话')`
   - 保留原 `id`
3. 创建 `messages_new`，外键指向 `sessions_new`，复制旧消息并保留 `id`。
4. 先 `DROP TABLE messages`，再 `DROP TABLE sessions`。
5. 将 `sessions_new` 重命名为 `sessions`，`messages_new` 重命名为 `messages`。
6. 创建索引和 partial unique index。
7. 创建 `chat_state`，把每个现有私聊会话设为 active。
8. 迁移在事务中执行；上线前先备份。

注意：

- 旧的私聊会话全部映射为 `scope='private'`。
- 当前没有群组数据，不需要补群组迁移。
- 迁移测试必须覆盖“0001 + 有用户/会话/消息数据 -> 0002”。

## 4. 仓储接口

建议把 `internal/session.Store` 调整为：

```go
type Scope string

const (
    ScopePrivate Scope = "private"
    ScopeGroup   Scope = "group"
)

type Store interface {
    // 私聊
    GetOrCreateActivePrivate(ctx context.Context, userID, chatID int64) (*Session, error)
    CreatePrivate(ctx context.Context, userID, chatID int64, title string) (*Session, error)
    ListPrivate(ctx context.Context, userID, chatID int64) ([]Session, error)
    GetPrivate(ctx context.Context, userID, chatID, sessionID int64) (*Session, error)
    SetActivePrivate(ctx context.Context, userID, chatID, sessionID int64) error
    RenamePrivate(ctx context.Context, userID, chatID, sessionID int64, title string) error
    DeletePrivate(ctx context.Context, userID, chatID, sessionID int64) error

    // 群组
    GetOrCreateGroup(ctx context.Context, userID, chatID int64) (*Session, error)

    // 通用
    GetByID(ctx context.Context, sessionID int64) (*Session, error)
    AppendMessage(ctx context.Context, sessionID int64, role, content string, tokens int) error
    RecentMessages(ctx context.Context, sessionID int64, limit int) ([]Message, error)
    SaveAnalysis(ctx context.Context, sessionID int64, symbol, market, analysisJSON string) (*Session, error)
    UpdateSummary(ctx context.Context, sessionID int64, summary string, tokens int) error
    TrimMessages(ctx context.Context, sessionID int64, keep int) error
    Reset(ctx context.Context, sessionID int64) (*Session, error)
    CleanupMessages(ctx context.Context, before time.Time) (int64, error)
}
```

约定：

- 所有私聊方法必须同时校验 `user_id + chat_id + scope`，防止 `/switch`、`/delete` 越权。
- `GetOrCreateActivePrivate`：
  - 有 active 且属于该用户/chat -> 返回。
  - 否则取最近一条私聊会话；没有则创建默认会话并设置 active。
- `CreatePrivate`：
  - 默认标题可为“会话 N”或首次分析的标的。
  - 每个用户/私聊最多 20 条（`MAX_PRIVATE_SESSIONS`）。
  - 创建后自动设为 active。
- `DeletePrivate`：
  - 不允许删除最后一个私聊会话；至少保留一条。
  - 删除 active 时，自动切到最近一条剩余会话。
- `Reset`：
  - 清空 `messages`、`summary`、`last_analysis_json`、`current_symbol`；
  - `generation++`；
  - 不取消在途分析。
- `GetOrCreateGroup`：
  - 按 `(chat_id,user_id,scope='group')` 查找；没有则创建。
  - 不设置 `chat_state`。

## 5. Telegram 路由与 UX

### 5.1 私聊

新增命令：

```text
/new [标题]          新建会话并设为 active
/sessions            列出当前私聊的所有会话
/switch <id>         切换 active 会话
/rename <id> <标题>  重命名
/delete <id>         删除；第一阶段要求追加 confirm
/reset               清空当前 active 会话
/status              显示 active 会话、标的、今日用量
```

`/sessions` 输出示例：

```text
1. BTC 短线  (active)
2. ETH 波段
3. 新会话

使用 /switch <id> 切换。
```

- 列表最多展示 20 条；按 `last_active_at DESC`。
- 标题最多 40 个字符，空标题自动生成。
- `/delete <id>` 第一阶段要求 `/delete <id> confirm`，避免误删；后续可换 inline keyboard。
- 只允许私聊使用 `/new`、`/sessions`、`/switch`、`/rename`、`/delete`。
- 普通发币种、`/analyze`、追问都作用于 active 会话。
- 分析开始时捕获 `sessionID`；后续结果、usage、消息都写这个 session。
- 切换 session 不会改变在途分析的目标。

### 5.2 群组

群组只响应以下消息：

- 带 `@bot_username` 的命令，例如 `/analyze@GuanLanAI_bot BTC`
- 回复 Bot 的消息
- `@bot_username` 提及（仅当 BotFather 关闭 privacy mode，或 Bot 是群管理员时才会投递）

Bot 需要在启动时保存 `GetMe().Username`，用于提及判断。privacy mode 开启时 Telegram 不会投递普通文本中的 @提及，因此群内默认引导用户使用带用户名的命令或回复 Bot 消息。

群组可用命令：

```text
/analyze BTC
/analyze@GuanLanAI_bot BTC
/status
/reset
/help
```

群组禁止：

- `/key`、`/key delete`
- `/baseurl`
- `/new`、`/sessions`、`/switch`、`/rename`、`/delete`

收到这些命令时提示：

```text
请在私聊中配置 Key 和管理会话。
```

群组会话行为：

- 每次处理群消息时用 `msg.From.ID` 找用户。
- `GetOrCreateGroup(userID, chatID)` 得到该成员唯一的群会话。
- 发言人必须已经配置 Key；否则提示私聊 `/key`。
- 分析消息和追问只读写该成员自己的 session，其他成员不可见。
- `/reset` 只清空发送者在该群的会话，不需要群管理员权限。
- 群成员之间的上下文完全隔离。
- 结果可以回复触发消息（`ReplyParameters`），作为后续优化；第一阶段可先不回复引用。

### 5.3 私聊 vs 群组判定

```text
chat.type == private  -> scope=private
chat.type == group/supergroup -> scope=group
chat.type == channel -> ignore
```

群组消息在进入 dispatcher 前做 mention/reply 过滤；未提及的消息不回复、不入库。

## 6. 并发与在途结果

- 现有 per-chat dispatcher 继续使用；群组里不同成员的消息仍然按 chat 串行。
- 分析开始时捕获 `sessionID`，之后所有写操作都使用该 ID，不重新读取 active pointer。
- 私聊切换会话：
  - 在途结果写回旧 session；
  - 新消息进入新 active session。
- `/reset`：
  - 清空当前 session 的已保存上下文；
  - 不取消在途分析；
  - 在途结果完成后仍写入该 session（产品已确认）。
- `/delete`：
  - 如果删除的是在途分析所属 session，结果无法持久化；仍可把卡片发给用户，但日志记录“会话已删除”，不自动重建。
- 群组里不同成员串行处理，长分析可能阻塞同群其他成员；这是当前 per-chat 队列的已知限制，后续可改成 per-user queue。

## 7. 安全与权限

- 私聊 session 操作必须校验 `user_id + chat_id + scope`，禁止只凭 session ID 操作。
- 群组不允许收集或修改 Key；Key 只能在私聊配置。
- 群组按 `From.ID` 隔离，不能读写其他成员 session。
- 群组消息未提及 Bot 时不处理、不存储。
- 群组 `/reset` 只影响发送者自己的 session。
- 群组结果仍使用发送者自己的 Key、模型、模式和限流额度。
- 私聊 `chat_state.active_session_id` 必须校验归属；删除会话时级联清理 messages。
- 会话标题会被 HTML 转义后展示。
- 数据保留 30 天；群组消息同样适用。

## 8. 限制与清理

- `MAX_PRIVATE_SESSIONS=20`（默认），超过时 `/new` 拒绝。
- 私聊标题最多 40 字符。
- 群组每个用户每个群固定一条，不设上限。
- 消息继续按 `DATA_RETENTION_DAYS` 清理。
- 可选：清理 90 天无活动且无消息的私聊非 active 会话。
- 私聊至少保留一条会话；删除最后一条时拒绝。

## 9. 测试计划

### 迁移

- 0001 建库并插入用户/私聊 session/messages。
- 执行 0002 后：
  - session 的 `scope='private'`；
  - 消息数量、ID、内容不变；
  - `chat_state` 指向原 session；
  - 群组 partial unique index 生效。

### 仓储

- 私聊创建、列表、切换、重命名、删除、上限。
- 删除 active 后自动切换。
- 群组同一用户同一群只有一条。
- 群 A/群 B 同一用户是两条。
- 群 A 不同用户是两条。
- 跨用户/跨 chat 的 session ID 操作被拒绝。
- `/reset` 清空消息/摘要/最近分析，generation 递增。
- 在途结果写回捕获的 session。

### Telegram

- 私聊 `/new`、`/sessions`、`/switch`、`/rename`、`/delete confirm`。
- 群组提及触发；未提及不回复。
- 群组 `/key` 被拒绝并提示私聊。
- 群组两个成员上下文隔离。
- 群组 `/reset` 只清空发送者。
- 在私聊切换会话不影响在途分析写回。

### 验收

- 两个测试账号在同一个群分别分析 BTC/ETH，互不串上下文。
- 私聊创建 2 条会话，切换后追问分别命中各自最近分析。
- `/reset` 后仍能收到在途分析结果，且结果写入原 session。
- `go test -race ./...` 全绿。

## 10. 分阶段实施

### Phase A：私聊多会话（约 2–3 天）

- 0002 迁移、session 仓储、active 指针。
- `/new`、`/sessions`、`/switch`、`/rename`、`/delete`。
- 现有私聊行为改为 active session。
- 迁移测试、仓储测试、私聊命令测试。

### Phase B：群组单会话（约 2–3 天）

- `GROUPS_ENABLED=false` 配置开关。
- 保存 bot username；群组 mention/reply 过滤。
- `GetOrCreateGroup`、群组命令限制、Key 引导。
- 群组隔离测试、两账号手工联调。
- 开启开关后部署 staging，再切 prod。

总计约 **4–6 个开发日**，建议预留 1–2 天缓冲。`/delete` inline keyboard、结果引用回复可作为后续增强。

## 11. 风险与开放问题

- **群组串行处理**：同一群多个成员同时分析会排队；当前先接受，后续可改 per-user queue。
- **群组隐私**：虽然按用户隔离，但群成员可能看到 Bot 回复；用户需知情。
- **群组滥用**：公开 Bot 可被拉群；当前只靠限流，后续可加群白名单/管理员开关。
- **迁移风险**：sessions 表重建涉及 messages 外键；上线前必须备份并做迁移测试。
- **删除在途会话**：`/delete` 可能使在途结果无法持久化；产品接受仅发送卡片、不重建。
- **开放问题**：
  - 群组是否允许 `/model`、`/mode`？本设计建议禁止，统一走私聊配置。
  - `/delete` 是否使用 inline keyboard 确认？
  - 私聊会话上限是否保持 20？
  - 群组是否需要白名单/管理员开启？

## 12. 验收标准

- [ ] 私聊可创建、列出、切换、重命名、删除多个会话。
- [ ] 一个用户在一个群里只有一条会话；不同成员互相隔离。
- [ ] 私聊和群组会话不会互相串上下文。
- [ ] 群组只能通过提及/回复触发，未提及消息不存储。
- [ ] 群组禁止配置 Key，提示走私聊。
- [ ] `/reset` 只清空当前会话，不丢弃在途结果。
- [ ] 迁移后旧私聊消息、摘要、最近分析仍在。
- [ ] `go test -race ./...` 全绿。
