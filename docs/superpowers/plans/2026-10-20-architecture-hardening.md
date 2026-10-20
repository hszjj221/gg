# gg 架构加固实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除审批的 fail-open 语义，让跨进程会话互斥在任何副作用发生前生效，并收敛重复的组合根与平台构建差异。

**Architecture:** 安全策略下沉到 `internal/agent`（nil approver 不再等于全部允许），各入口只负责提供"人类应答器"；`session.Store` 增加 turn 级写租约，`Service.run` 在 turn 开始时获取；组合根抽到 `internal/bootstrap`，Media 配置映射收敛到 `config.Config`。

**Tech Stack:** Go 1.26、标准库、`go test`/`go test -race`、GitHub Actions。

**Spec:** 本文档的 Problem Statement（源自 2026-10-20 架构评审）。`internal/cliapp` 业务用例迁移（job/artifact/library/kb/memory）不在本计划范围，单独制定计划。

## Problem Statement

1. `agent.Runner` 在 `Approver == nil` 时跳过全部审批，包括 `Risk=dangerous` 的命令（`internal/agent/runner.go:286`）。HTTP `run.start` 传 `requireApproval:false` 时 Manager 注入 nil（`internal/app/runtime.go:209-215`），因此危险命令在该入口直接执行，而 CLI `--yolo` 和 scheduler `--allow-all` 都会拒绝。
2. 跨进程的"一个 session 一个活跃 turn"只在写盘时检测（`internal/session/session.go:364-433`），provider 调用和工具副作用已经发生。写租约的 stale 接管还有 TOCTOU（`internal/session/lock.go:44-51`）。
3. Media 客户端、provider 工厂、repository、skills 加载在 CLI、job、daemon 中重复构造，已出现行为差异。
4. `internal/daemon/pidfile.go` 直接使用 `syscall.Flock`，`GOOS=windows go build ./...` 失败；CI 没有跨平台编译检查。
5. fork/clone 在 `manager.Add` 失败时不关闭子 Service（`internal/app/host.go:338,347`），泄漏 MCP/Chromium 资源。

## Global Constraints

- 不改变既有 JSON-RPC 方法与线格式；`requireApproval` 仍是合法参数。
- `Risk=readonly` 在**所有**入口都必须无需审批直接运行。
- `Risk=dangerous` 与 `Risk=confirm` 在**没有人类应答**时必须拒绝，包括 `--yolo`、`--allow-all`、`requireApproval:false`。
- 不新增第三方依赖。
- 每个任务结束必须通过：`go test ./...`、`go vet ./...`；涉及 session/agent 的任务额外跑 `go test -race ./internal/agent ./internal/app ./internal/session`。
- 提交信息遵循仓库现有 `feat:` / `fix:` / `test:` / `docs:` 前缀。

## Review Focus

- 客户端传 `requireApproval:false` 时，`rm -rf ~` 仍被拒绝；普通 `write` 仍可执行（不把"关闭审批"变成"关闭全部工具"）。
- 没有 approver 的嵌入方（测试、subagent、库调用）不得因为新增策略而阻塞普通工具。
- CLI 与 daemon 同时向同一 session 发 turn：第二个进程必须在任何 provider 请求或工具执行之前失败。
- 一个等待人工审批超过 30 秒的 turn，其写租约不得被另一个进程抢走。
- Chromium/MCP 未安装时，新增的租约与审批逻辑不得改变"能力降级为缺失、turn 继续"的行为。

---

### Task 1: nil approver 的 fail-closed 策略

**Files:**
- Modify: `internal/agent/runner.go:283-325`
- Modify: `internal/agent/types.go`（在 `Approver` 附近新增策略实现）
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `ApprovalRequest.Risk`、`ApprovalRiskReadOnly`、`ApprovalRiskDangerous`、`ApprovalRiskConfirm`（均已存在）。
- Produces: `agent.DefaultApprover`（值类型 `defaultApprover`），后续 app/telegram 测试直接引用其行为。

- [ ] **Step 1: 写失败测试**

在 `internal/agent/agent_test.go` 追加：

```go
func TestNilApproverDeniesDangerousTool(t *testing.T) {
	tool := &fakeApprovalTool{name: "danger", risk: ApprovalRiskDangerous}
	provider := &fakeProvider{responses: []AssistantMessage{
		{
			Message:    Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "danger", Arguments: json.RawMessage(`{}`)}}},
			StopReason: StopReasonToolUse,
		},
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	runner := NewRunner(provider, []Tool{tool})
	if _, err := runner.Run(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if tool.runs != 0 {
		t.Fatalf("dangerous tool ran %d times without an approver", tool.runs)
	}
	// The denial must reach the model as a tool result, not be silent.
	last := provider.requests[len(provider.requests)-1]
	found := false
	for _, msg := range last.Messages {
		if msg.Role == RoleTool && strings.Contains(msg.Content, "no approver") {
			found = true
		}
	}
	if !found {
		t.Fatalf("model did not receive the denial: %+v", last.Messages)
	}
}

func TestNilApproverAllowsOrdinaryAndReadOnlyTools(t *testing.T) {
	for _, risk := range []string{"", ApprovalRiskReadOnly} {
		tool := &fakeApprovalTool{name: "ok", risk: risk}
		provider := &fakeProvider{responses: []AssistantMessage{
			{
				Message:    Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "ok", Arguments: json.RawMessage(`{}`)}}},
				StopReason: StopReasonToolUse,
			},
			{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
		}}
		runner := NewRunner(provider, []Tool{tool})
		if _, err := runner.Run(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		if tool.runs != 1 {
			t.Fatalf("risk %q: tool ran %d times, want 1", risk, tool.runs)
		}
	}
}
```

`fakeProvider` 会把每次请求记入 `requests`；`ToolResult` 由 runner 转成 `RoleTool` 消息，因此第二次请求的 `Messages` 必须包含拒绝文本。

`fakeApprovalTool` 增加字段并让 `ApprovalRequest` 返回它：

```go
type fakeApprovalTool struct {
	name        string
	runs        int
	risk        string
	approvalErr error
}

func (t *fakeApprovalTool) ApprovalRequest(args json.RawMessage) (ApprovalRequest, error) {
	if t.approvalErr != nil {
		return ApprovalRequest{}, t.approvalErr
	}
	return ApprovalRequest{ToolName: t.name, Summary: "run " + t.name, Details: string(args), Arguments: args, Risk: t.risk}, nil
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent -run 'TestNilApprover' -count=1`
Expected: `TestNilApproverDeniesDangerousTool` FAIL（tool 被执行）。

- [ ] **Step 3: 实现策略**

在 `internal/agent/types.go` 的 `Approver` 接口之后新增：

```go
// DefaultApprover is the policy used when a caller supplies no approver.
// It never auto-approves a call that needs a human: dangerous and confirm
// risks are denied, everything else (including read-only) keeps running.
// A nil approver must therefore not mean "allow everything".
type DefaultApprover struct{}

func (DefaultApprover) Approve(_ context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	switch req.Risk {
	case ApprovalRiskDangerous:
		return ApprovalDecision{Allow: false, Reason: "dangerous command denied: no approver is configured"}, nil
	case ApprovalRiskConfirm:
		return ApprovalDecision{Allow: false, Reason: "command needs confirmation but no approver is configured"}, nil
	default:
		return ApprovalDecision{Allow: true}, nil
	}
}
```

`types.go` 已 import `context`；若没有，补上。

修改 `internal/agent/runner.go` 的审批段：

```go
	approver := r.approver
	if approver == nil {
		// Fail closed: a missing approver is not permission to run
		// approval-gated calls. The tool is still denied only for risks
		// that require a human; ordinary tools keep working.
		approver = DefaultApprover{}
	}
	if _, ok := tool.(ApprovalDescriber); ok {
		// ... existing req population ...
		if !req.PreApproved {
			decision, err := approver.Approve(ctx, req)
			// ... existing handling unchanged ...
		}
	}
```

删除原来的 `if r.approver != nil { ... }` 包裹层，改为上面的 `approver` 局部变量。`PreApproved` 仍跳过审批。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/runner.go internal/agent/types.go internal/agent/agent_test.go
git commit -m "fix(agent): fail closed when no approver is configured"
```

---

### Task 2: HTTP `requireApproval:false` 的端到端语义

**Files:**
- Test: `internal/app/runtime_test.go`（或新建 `internal/app/approval_policy_test.go`）
- Modify: `internal/transport/jsonrpc/handler.go:17-23`（注释）
- Modify: `docs/architecture.md` 的安全表

**Interfaces:**
- Consumes: `Manager.StartTurn(ctx, sessionID, prompt, requireApproval bool)`、`Service.Run(ctx, prompt, onEvent, approver)`。
- Produces: 一个回归测试，固定"关闭审批不等于关闭安全策略"的语义。

- [ ] **Step 1: 写失败测试**

在 `internal/app` 新增测试（复用该包已有的 fake provider 构造方式；若没有，用 `ProviderFactory` 返回一个直接发起工具调用的 provider）：

```go
// toolCallProvider asks for one tool by name, then ends the turn.
type toolCallProvider struct {
	name  string
	calls int
}

func (p *toolCallProvider) Complete(_ context.Context, _ agent.Request, _ func(agent.Event)) (agent.AssistantMessage, error) {
	p.calls++
	if p.calls == 1 {
		return agent.AssistantMessage{
			Message: agent.Message{
				Role:      agent.RoleAssistant,
				ToolCalls: []agent.ToolCall{{ID: "c1", Name: p.name, Arguments: json.RawMessage(`{}`)}},
			},
			StopReason: agent.StopReasonToolUse,
		}, nil
	}
	return agent.AssistantMessage{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: "done"},
		StopReason: agent.StopReasonEndTurn,
	}, nil
}

type dangerousFakeTool struct{ ran *bool }

func (t *dangerousFakeTool) Name() string { return "dangerous" }
func (t *dangerousFakeTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{Name: "dangerous", Description: "test", Parameters: map[string]any{"type": "object"}}
}
func (t *dangerousFakeTool) Execute(context.Context, json.RawMessage) agent.ToolResult {
	*t.ran = true
	return agent.ToolResult{Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "ran"}}}
}
func (t *dangerousFakeTool) ApprovalRequest(json.RawMessage) (agent.ApprovalRequest, error) {
	return agent.ApprovalRequest{ToolName: "dangerous", Risk: agent.ApprovalRiskDangerous}, nil
}

// runtimeTestManagerWithTools mirrors runtimeTestManagerWithOptions but
// injects the tool registry that owns the fake capability.
func runtimeTestManagerWithTools(t *testing.T, provider agent.Provider, providers []ToolProvider) (*Manager, string) {
	t.Helper()
	manager := NewManagerWithOptions(ManagerOptions{})
	cwd := t.TempDir()
	store, err := session.NewStore(filepath.Join(cwd, "session.jsonl"), cwd)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{
		Config:          config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return provider },
		Store:           store,
		ModelRecorded:   true,
		ToolProviders:   providers,
	})
	sessionID, err := manager.Add(service)
	if err != nil {
		t.Fatal(err)
	}
	return manager, sessionID
}

func TestStartTurnWithoutApprovalStillDeniesDangerousTools(t *testing.T) {
	ran := false
	tool := &dangerousFakeTool{ran: &ran}
	manager, sessionID := runtimeTestManagerWithTools(t,
		&toolCallProvider{name: "dangerous"},
		[]ToolProvider{{
			Name: "test",
			Build: func(context.Context, ToolContext) ([]agent.Tool, error) {
				return []agent.Tool{tool}, nil
			},
		}},
	)

	run, err := manager.StartTurn(context.Background(), sessionID, "go", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Await(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("dangerous tool ran with requireApproval=false")
	}
}
```

`internal/app/runtime_test.go` 已经 import 了 `context`、`filepath`、`agent`、`config`、`session`；新增测试还需要 `encoding/json`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/app -run TestStartTurnWithoutApprovalStillDeniesDangerousTools -count=1`
Expected: FAIL（`dangerous tool ran with requireApproval=false`）。

- [ ] **Step 3: 更新注释与文档**

`internal/transport/jsonrpc/handler.go` 的协议注释追加：

```go
// 1.5: requireApproval=false disables interactive prompts for ordinary
// tools only. Dangerous and confirm-risk calls are still denied by the
// agent's default policy when no approver is available.
```

`docs/architecture.md` 的安全表在 `never` 行之后新增一段：

```markdown
`requireApproval:false` from a transport client does not disable policy:
calls classified `dangerous` or `confirm` are denied when no human approver
is attached, exactly like an unattended scheduled run.
```

同时更新 `docs/architecture.zh-CN.md` 的对应段落。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/app ./internal/transport/... -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/app internal/transport/jsonrpc/handler.go docs/architecture.md docs/architecture.zh-CN.md
git commit -m "test(app): pin fail-closed behavior for requireApproval=false"
```

---

### Task 3: 统一无人值守审批策略并修正只读工具语义

**Files:**
- Modify: `internal/scheduler/scheduler.go:26-56`
- Modify: `internal/telegram/bot.go:427-433,469,493`
- Test: `internal/agent/agent_test.go`、`internal/scheduler/scheduler_test.go`

**Interfaces:**
- Produces: `agent.UnattendedApprover{AllowAll bool}`；`scheduler.UnattendedApprover` 变成类型别名，CLI/daemon 调用点无需修改。
- Consumes: `ApprovalRiskReadOnly`、`ApprovalRiskDangerous`、`ApprovalRiskConfirm`。

- [ ] **Step 1: 写失败测试**

在 `internal/agent/agent_test.go` 追加：

```go
func TestUnattendedApproverAllowsReadOnly(t *testing.T) {
	got, err := UnattendedApprover{}.Approve(context.Background(), ApprovalRequest{Risk: ApprovalRiskReadOnly})
	if err != nil || !got.Allow {
		t.Fatalf("read-only = %+v, %v; want allow", got, err)
	}
}

func TestUnattendedApproverDeniesWithoutAllowAll(t *testing.T) {
	got, err := UnattendedApprover{}.Approve(context.Background(), ApprovalRequest{})
	if err != nil || got.Allow {
		t.Fatalf("ordinary = %+v, %v; want deny", got, err)
	}
	got, _ = UnattendedApprover{AllowAll: true}.Approve(context.Background(), ApprovalRequest{})
	if !got.Allow {
		t.Fatal("allow-all should allow ordinary tools")
	}
}

func TestUnattendedApproverNeverAllowsDangerous(t *testing.T) {
	for _, risk := range []string{ApprovalRiskDangerous, ApprovalRiskConfirm} {
		got, _ := UnattendedApprover{AllowAll: true}.Approve(context.Background(), ApprovalRequest{Risk: risk})
		if got.Allow {
			t.Fatalf("risk %q allowed unattended", risk)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent -run TestUnattendedApprover -count=1`
Expected: 编译失败（`undefined: UnattendedApprover`）。

- [ ] **Step 3: 移动实现并保留别名**

把 `internal/scheduler/scheduler.go` 的 `UnattendedApprover` 实现原样移入 `internal/agent/types.go`，并在文件顶部补 `context` import（若尚未导入），同时在 risk 分支前加：

```go
	if req.Risk == ApprovalRiskReadOnly {
		return ApprovalDecision{Allow: true}, nil
	}
```

在 `internal/scheduler/scheduler.go` 原来的类型位置替换为：

```go
// UnattendedApprover is the scheduled-run approval policy. The canonical
// implementation lives in agent so every unattended entry point shares it.
type UnattendedApprover = agent.UnattendedApprover
```

删除 scheduler 中不再使用的 `fmt` import（若其他函数仍用则保留）。

- [ ] **Step 4: Telegram 改用共享策略**

`internal/telegram/bot.go`：

```go
	run, err := b.ws.StartTurnWithApprover(ctx, binding.SessionID, prompt, agent.UnattendedApprover{})
```

两处调用（469、493）都替换；删除 `denyAllApprover` 类型。`internal/telegram` 已 import `agent`，无需新增。这样只读 bash（`ls`、`git status`）在 Telegram 上可用，而危险命令仍被拒。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/agent ./internal/scheduler ./internal/telegram -count=1`
Expected: PASS；scheduler 既有 `TestUnattendedApprover*` 测试仍然通过。

- [ ] **Step 6: 提交**

```bash
git add internal/agent/types.go internal/agent/agent_test.go internal/scheduler/scheduler.go internal/telegram/bot.go
git commit -m "fix(approval): share one unattended policy and allow read-only calls"
```

---

### Task 4: 写租约的原子接管与 PID 存活检查

**Files:**
- Modify: `internal/session/lock.go:44-51`
- Create: `internal/session/lock_process_unix.go`
- Create: `internal/session/lock_process_other.go`
- Test: `internal/session/lock_test.go`（新建）

**Interfaces:**
- Produces: `lockIsStale(path string, now time.Time) bool`、`lockOwnerPID(path string) (int, bool)`、`processAlive(pid int) bool`、`createLockFile(lockPath, token string) (func(), error)`、`tryAcquireWriterLock(sessionPath string) (func(), error)`。最后一个供 Task 5 的 turn 租约使用：非阻塞，持有者存活时立即返回 `ErrLocked`。
- Consumes: 锁文件内容格式保持 `"<pid>:<random>"`（`lock.go:22` 已如此）。

- [ ] **Step 1: 写失败测试**

新建 `internal/session/lock_test.go`：

```go
package session

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLockIsStaleRequiresDeadOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl.lock")
	if err := os.WriteFile(path, []byte("999999:token"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if !lockIsStale(path, time.Now()) {
		t.Fatal("old lock held by a dead pid should be stale")
	}
}

func TestLockIsNotStaleWhileOwnerIsAlive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl.lock")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+":token"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if lockIsStale(path, time.Now()) {
		t.Fatal("a live process's lock must not be stolen")
	}
}

func TestAcquireWriterLockTakesOverDeadLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, []byte("999999:token"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := acquireWriterLock(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
```

`itoa` 用 `strconv.Itoa`，在测试文件里 import `strconv` 后直接调用，不要新增 helper。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/session -run 'TestLock|TestAcquireWriterLockTakesOver' -count=1`
Expected: 编译失败（`undefined: lockIsStale`）。

- [ ] **Step 3: 实现 PID 存活检查**

`internal/session/lock_process_unix.go`：

```go
//go:build unix

package session

import "syscall"

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
```

`internal/session/lock_process_other.go`：

```go
//go:build !unix

package session

// processAlive is conservative on platforms without a cheap liveness
// probe: report true so a lock is never stolen on an assumption we cannot
// verify. gg officially supports Linux and macOS.
func processAlive(pid int) bool { return true }
```

- [ ] **Step 4: 实现 stale 判定与原子接管**

在 `internal/session/lock.go` 增加：

```go
func lockIsStale(path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil || now.Sub(info.ModTime()) <= staleLockAge {
		return false
	}
	pid, ok := lockOwnerPID(path)
	if !ok {
		// Unreadable/corrupt lease: it cannot name a live owner, so it is
		// recoverable exactly like an aged lock.
		return true
	}
	return !processAlive(pid)
}

func lockOwnerPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	head, _, ok := strings.Cut(strings.TrimSpace(string(data)), ":")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(head)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}
```

`lock.go` 需要新增 `strconv` import（`strings` 已存在）。

把创建锁文件的代码抽成 `createLockFile`：

```go
func createLockFile(lockPath, token string) (func(), error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err = file.WriteString(token); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(lockPath)
		if err != nil {
			return nil, err
		}
		return nil, closeErr
	}
	return func() {
		data, readErr := os.ReadFile(lockPath)
		if readErr == nil && strings.TrimSpace(string(data)) == token {
			_ = os.Remove(lockPath)
		}
	}, nil
}
```

`acquireWriterLock` 改为每轮调用它，并把 stale 分支替换为原子接管：

```go
		release, err := createLockFile(lockPath, token)
		if err == nil {
			return release, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if lockIsStale(lockPath, time.Now()) {
			// Move the old lease out of the way atomically. Only one
			// contender can rename it; a successor's fresh lock at the
			// original path is never removed by this code.
			stalePath := lockPath + ".stale." + newID()
			if renameErr := os.Rename(lockPath, stalePath); renameErr == nil {
				_ = os.Remove(stalePath)
			}
			continue
		}
```

保留原有超时与轮询逻辑。

新增非阻塞版本（turn 租约使用；第二个进程必须立刻失败，而不是等 5 秒）：

```go
// tryAcquireWriterLock is acquireWriterLock without the retry loop. A live
// owner yields ErrLocked immediately; an aged lease whose owner is gone is
// taken over once.
func tryAcquireWriterLock(sessionPath string) (func(), error) {
	lockPath := sessionPath + ".lock"
	token := fmt.Sprintf("%d:%s", os.Getpid(), newID())
	release, err := createLockFile(lockPath, token)
	if err == nil {
		return release, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	if !lockIsStale(lockPath, time.Now()) {
		return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
	}
	stalePath := lockPath + ".stale." + newID()
	if err := os.Rename(lockPath, stalePath); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
	}
	_ = os.Remove(stalePath)
	return createLockFile(lockPath, token)
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/session -run 'TestLock|TestAcquireWriterLock|TestStore' -count=1`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/session/lock.go internal/session/lock_process_*.go internal/session/lock_test.go
git commit -m "fix(session): stop stealing live writer leases"
```

---

### Task 5: turn 级跨进程写租约

**Files:**
- Modify: `internal/session/session.go:74-80`（`Store` 结构）
- Modify: `internal/session/session.go:364-406`（`appendRecord`）
- Modify: `internal/app/service.go:60-75`（`run`）
- Test: `internal/session/session_test.go`、`internal/app/modularity_test.go`

**Interfaces:**
- Produces: `(*session.Store).AcquireTurnLease() (release func(), err error)`。同一 Store 重复调用返回 `ErrLocked`；release 幂等。
- Consumes: `tryAcquireWriterLock`（Task 4）、`validateCurrentLocked`。

- [ ] **Step 1: 写失败测试**

在 `internal/session/session_test.go` 追加：

```go
func TestTurnLeaseIsExclusiveAcrossStores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	first, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := first.AcquireTurnLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.AcquireTurnLease(); err == nil {
		t.Fatal("second store acquired a lease held by the first")
	}
	release()
	release() // idempotent
	release2, err := second.AcquireTurnLease()
	if err != nil {
		t.Fatal(err)
	}
	release2()
}

func TestAppendUsesHeldTurnLease(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "s.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.AcquireTurnLease()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := store.AppendModel("p", "m"); err != nil {
		t.Fatalf("append under own lease: %v", err)
	}
}
```

在 `internal/app/modularity_test.go` 追加一个端到端断言：租约被另一个 Store 持有时，`Service.Run` 必须在 provider 被调用之前失败。

```go
func TestServiceRunFailsBeforeProviderWhenLeaseHeld(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "session.jsonl")
	store, err := session.NewStore(path, cwd)
	if err != nil {
		t.Fatal(err)
	}
	other, err := session.NewStore(path, cwd)
	if err != nil {
		t.Fatal(err)
	}
	release, err := other.AcquireTurnLease()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	provider := &runtimeProvider{}
	svc := NewService(Options{
		Config:          config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return provider },
		Store:           store,
		ModelRecorded:   true,
	})
	defer svc.Close()

	if _, err := svc.Run(context.Background(), "hello", nil, nil); err == nil {
		t.Fatal("run succeeded while another store held the turn lease")
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times before the lease check", provider.calls)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/session -run 'TestTurnLease|TestAppendUsesHeldTurnLease' -count=1`
Expected: 编译失败（`AcquireTurnLease` 未定义）。

- [ ] **Step 3: 实现 Store 租约**

`Store` 结构新增：

```go
	// leaseRelease is non-nil while a turn holds the cross-process writer
	// lease. appendRecord then reuses it instead of re-locking.
	leaseRelease func()
```

新增方法：

```go
// AcquireTurnLease holds the session's cross-process writer lease for the
// lifetime of one agent turn. It must be released even when the turn fails.
// A second store (CLI vs daemon, or two daemons) gets ErrLocked before any
// provider call or tool side effect happens.
func (s *Store) AcquireTurnLease() (func(), error) {
	s.mu.Lock()
	if s.leaseRelease != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrLocked, s.path)
	}
	release, err := tryAcquireWriterLock(s.path)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if err := s.validateCurrentLocked(); err != nil {
		release()
		s.mu.Unlock()
		return nil, err
	}
	s.leaseRelease = release
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			release := s.leaseRelease
			s.leaseRelease = nil
			s.mu.Unlock()
			if release != nil {
				release()
			}
		})
	}, nil
}
```

`appendRecord` 在 `s.mu.Lock()` 之后改为：

```go
	if s.leaseRelease == nil {
		release, err := acquireWriterLock(s.path)
		if err != nil {
			return err
		}
		defer release()
	}
```

后续 `validateCurrentLocked`、写入、更新 `diskInfo` 逻辑保持不变。

- [ ] **Step 4: 在 turn 边界获取租约**

`internal/app/service.go` 的 `Service.run`：

```go
func (s *Service) run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	if err := s.beginTurn(); err != nil {
		return Result{}, err
	}
	defer s.endTurn()
	if s.store != nil {
		// Holding the lease for the whole turn means a second process
		// fails before the provider is called, not after side effects.
		release, err := s.store.AcquireTurnLease()
		if err != nil {
			return Result{}, fmt.Errorf("session is busy in another process: %w", err)
		}
		defer release()
	}
	if result, ok, err := s.handleControlCommand(ctx, prompt); ok || err != nil {
		return result, err
	}
	return s.execute(ctx, prompt, onEvent, approver)
}
```

`service.go` 需要 import `fmt`（若尚未导入）。`s.store` 在 `running` 为 true 期间不会被 fork/clone 替换，这一点写进注释。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/session ./internal/app -count=1`
Expected: PASS。重点确认既有 `TestServiceSerializesDirectTurns`、recovery 测试不回归。

- [ ] **Step 6: 提交**

```bash
git add internal/session/session.go internal/session/session_test.go internal/app/service.go
git commit -m "fix(session): hold a turn lease before provider and tool work"
```

---

### Task 6: 统一 Media 客户端构造

**Files:**
- Modify: `internal/config/config.go`（新增方法）
- Modify: `internal/app/toolregistry.go:95-115`
- Modify: `internal/cliapp/media.go:22-40`
- Modify: `internal/daemon/telegram.go:16-28`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `func (c Config) MediaClientConfig() (media.Config, bool)`；`false` 表示没有可用 base URL。
- Consumes: `media.Config`。

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 追加：

```go
func TestMediaClientConfigFallsBackToProvider(t *testing.T) {
	cfg := Config{
		BaseURL:       "https://api.example.com/v1",
		APIKey:        "sk-test",
		HomeDir:       "/home/u",
		MediaImageModel: "img",
	}
	mc, ok := cfg.MediaClientConfig()
	if !ok {
		t.Fatal("expected media config")
	}
	if mc.BaseURL != "https://api.example.com/v1" || mc.APIKey != "sk-test" {
		t.Fatalf("fallback = %+v", mc)
	}
	if mc.Dir != filepath.Join("/home/u", ".gg", "media") {
		t.Fatalf("dir = %q", mc.Dir)
	}
}

func TestMediaClientConfigRequiresBaseURL(t *testing.T) {
	if _, ok := (Config{}).MediaClientConfig(); ok {
		t.Fatal("empty config should not produce a media client")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config -run TestMediaClientConfig -count=1`
Expected: 编译失败（方法未定义）。

- [ ] **Step 3: 实现配置映射**

`internal/config/config.go` 新增（import `internal/media`；`media` 不 import `config`，不会成环）：

```go
// MediaClientConfig maps the resolved configuration to the media client's
// inputs. The second result is false when no base URL is available, which
// means media tools and commands should be unavailable rather than built
// against an empty endpoint.
func (c Config) MediaClientConfig() (media.Config, bool) {
	baseURL := c.MediaBaseURL
	if baseURL == "" {
		baseURL = c.BaseURL
	}
	if baseURL == "" {
		return media.Config{}, false
	}
	apiKey := c.MediaAPIKey
	if apiKey == "" {
		apiKey = c.APIKey
	}
	return media.Config{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		ImageModel: c.MediaImageModel,
		TTSModel:   c.MediaTTSModel,
		STTModel:   c.MediaSTTModel,
		Dir:        filepath.Join(c.HomeDir, ".gg", "media"),
	}, true
}
```

- [ ] **Step 4: 替换三处构造**

`internal/app/toolregistry.go` 的 `MediaClient`：保留 memo key 计算，只把 `media.NewClient(media.Config{...})` 替换为：

```go
	mc, ok := cfg.MediaClientConfig()
	if !ok {
		m.mediaClient = nil
		m.mediaKey = ""
		return nil
	}
	if m.mediaClient == nil || m.mediaKey != key {
		m.mediaKey = key
		m.mediaClient = media.NewClient(mc)
	}
```

`internal/cliapp/media.go`：

```go
func mediaClientForCLI(cfg config.Config) *media.Client {
	mc, ok := cfg.MediaClientConfig()
	if !ok {
		return nil
	}
	return media.NewClient(mc)
}
```

`runMediaCommand` 在 `client == nil` 时打印错误并返回 1：

```go
	client := mediaClientForCLI(cfg)
	if client == nil {
		fmt.Fprintln(stderr, "media is not configured: set GG_MEDIA_BASE_URL or a provider base URL")
		return 1
	}
```

`internal/daemon/telegram.go`：

```go
	var mclient *media.Client
	if mc, ok := cfg.MediaClientConfig(); ok {
		mclient = media.NewClient(mc)
	}
```

删除不再使用的 `path/filepath`、`nonEmpty` 调用（若 `nonEmpty` 在该文件其他地方仍用则保留）。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/config ./internal/app ./internal/cliapp ./internal/daemon -count=1`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/config internal/app/toolregistry.go internal/cliapp/media.go internal/daemon/telegram.go
git commit -m "refactor(config): centralize media client construction"
```

---

### Task 7: 组合根收敛到 bootstrap

**Files:**
- Create: `internal/bootstrap/bootstrap.go`
- Create: `internal/bootstrap/bootstrap_test.go`
- Modify: `internal/cliapp/cliapp.go:160-190,352-379`
- Modify: `internal/cliapp/job.go:345-380`
- Modify: `internal/daemon/run.go:150-170`

**Interfaces:**
- Produces:
  - `bootstrap.ProviderFactory(override app.ProviderFactory) app.ProviderFactory`
  - `bootstrap.Repository(cfg config.Config) *session.FileRepository`
  - `bootstrap.LoadSkills(cwd, home string, disabled bool) (skills.Set, error)`
- Consumes: `provider.New`、`session.NewFileRepository`、`skills.Load`。

- [ ] **Step 1: 写失败测试**

`internal/bootstrap/bootstrap_test.go`：

```go
package bootstrap

import (
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
)

func TestProviderFactoryUsesOverride(t *testing.T) {
	called := false
	override := func(config.Config) agent.Provider { called = true; return nil }
	f := ProviderFactory(override)
	f(config.Config{})
	if !called {
		t.Fatal("override was not used")
	}
}

func TestProviderFactoryDefaultsToProviderNew(t *testing.T) {
	if ProviderFactory(nil) == nil {
		t.Fatal("default factory is nil")
	}
}

func TestLoadSkillsDisabled(t *testing.T) {
	set, err := LoadSkills(t.TempDir(), t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Available()) != 0 {
		t.Fatalf("disabled skills = %d", len(set.Available()))
	}
}

var _ app.ProviderFactory = ProviderFactory(nil)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/bootstrap -count=1`
Expected: FAIL（包不存在）。

- [ ] **Step 3: 实现 bootstrap**

`internal/bootstrap/bootstrap.go`：

```go
// Package bootstrap builds the dependencies shared by the CLI and daemon
// composition roots. It exists so provider, repository, and skill
// construction cannot drift between entry points.
package bootstrap

import (
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
)

// ProviderFactory returns the caller's override when supplied, otherwise the
// single provider factory. The CLI, job runner, and daemon all use this.
func ProviderFactory(override app.ProviderFactory) app.ProviderFactory {
	if override != nil {
		return override
	}
	return provider.New
}

// Repository returns the file-backed session repository for cfg.
func Repository(cfg config.Config) *session.FileRepository {
	return session.NewFileRepository(cfg.SessionDir)
}

// LoadSkills loads the skill set unless skills are disabled.
func LoadSkills(cwd, home string, disabled bool) (skills.Set, error) {
	if disabled {
		return skills.Set{}, nil
	}
	return skills.Load(skills.LoadOptions{CWD: cwd, HomeDir: home})
}
```

- [ ] **Step 4: 替换调用点**

`internal/cliapp/cliapp.go`：

```go
	providerFactory := bootstrap.ProviderFactory(options.ProviderFactory)
```

skills 段替换为：

```go
	skillSet, err := bootstrap.LoadSkills(cfg.CWD, options.HomeDir, parsed.NoSkills)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
```

`internal/cliapp/job.go` 的 skills 与 provider 段同样替换。

`internal/daemon/run.go`：

```go
	providerFactory := bootstrap.ProviderFactory(options.ProviderFactory)
	...
	rt, err := app.NewRuntime(app.RuntimeOptions{
		...
		Repository: bootstrap.Repository(cfg),
		...
	})
```

删除三个文件里现在未使用的 `provider` / `skills` import。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/bootstrap ./internal/cliapp ./internal/daemon -count=1`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/bootstrap internal/cliapp internal/daemon/run.go
git commit -m "refactor: share one bootstrap for CLI and daemon dependencies"
```

---

### Task 8: Manager.Add 的错误路径释放 Service

**Files:**
- Modify: `internal/app/runtime.go:152-180`
- Modify: `internal/app/service.go:60-75`
- Modify: `internal/app/service_factory.go:44-72`
- Test: `internal/app/runtime_test.go`

**Interfaces:**
- Produces: `Service.Close()` 幂等；`Service.Run` 在关闭后返回 `ErrorRuntimeClosed`；`Manager.Add` 在所有错误路径调用 `Close`。
- Consumes: `conversationTools.Close`。

- [ ] **Step 1: 写失败测试**

在 `internal/app/runtime_test.go` 追加：

```go
func TestManagerAddClosesServiceWithoutSession(t *testing.T) {
	manager := NewManagerWithOptions(ManagerOptions{})
	service := NewService(Options{
		Config: config.Config{CWD: t.TempDir(), Selection: "test:model"},
	})
	if _, err := manager.Add(service); err == nil {
		t.Fatal("Add accepted a service without a persisted session")
	}
	_, err := service.Run(context.Background(), "hello", nil, nil)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorRuntimeClosed {
		t.Fatalf("service was not closed after Add failed: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/app -run TestManagerAddClosesServiceWithoutSession -count=1`
Expected: FAIL（`service was not closed` 或 Run 没有 closed 检查）。

- [ ] **Step 3: 统一关闭责任并加关闭标志**

`internal/app/runtime.go` 的 `getOrAdd`：`id == ""` 分支先关闭再返回：

```go
	id := service.Snapshot().SessionID
	if id == "" {
		_ = service.Close()
		return nil, fmt.Errorf("conversation must have a persisted session")
	}
```

这样 `host.go` 的 fork/clone 调用点无需再各写一份清理，`m.closed` 与 `existing != service` 两条路径原有的 Close 保持不变。

`Service` 增加关闭状态（`service.go`）：

```go
type Service struct {
	*turnExecutor
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.closeErr = s.tools.Close()
	})
	return s.closeErr
}
```

`Service.Run` 开头：

```go
	if s.closed.Load() {
		return Result{}, errorf(ErrorRuntimeClosed, false, "conversation is closed")
	}
```

`service_factory.go` 的构造改为命名字段：

```go
	return &Service{turnExecutor: &turnExecutor{...}}
```

import `sync`、`sync/atomic`。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/app -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/app/runtime.go internal/app/service.go internal/app/service_factory.go internal/app/runtime_test.go
git commit -m "fix(app): close services rejected by the manager"
```

---

### Task 9: pidfile 跨平台构建

**Files:**
- Modify: `internal/daemon/pidfile.go`
- Create: `internal/daemon/pidfile_lock_unix.go`
- Create: `internal/daemon/pidfile_lock_other.go`
- Test: 现有 `internal/daemon/pidfile_test.go`

**Interfaces:**
- Produces: `lockPIDFile(f *os.File) error`、`unlockPIDFile(f *os.File) error`、`errPIDLockUnsupported`。
- Consumes: `syscall.Flock` 只出现在 unix 文件。

- [ ] **Step 1: 写失败检查**

Run: `GOOS=windows GOARCH=amd64 go build ./...`
Expected: FAIL，`undefined: syscall.Flock`（已有证据）。

- [ ] **Step 2: 抽出平台实现**

`pidfile.go` 删除 `syscall` import，替换三处调用：

```go
	if err := lockPIDFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errPIDLockUnsupported) {
			return nil, fmt.Errorf("pidfile locking is unavailable on this platform")
		}
		return nil, fmt.Errorf("lock pid file: %w", err)
	}
```

`EWOULDBLOCK` 的翻译放在平台函数里：`lockPIDFile` 在锁被占用时直接返回 `ErrAlreadyRunning`，公共文件不再引用 `syscall`。

`pidfile_lock_unix.go`：

```go
//go:build unix

package daemon

import (
	"errors"
	"os"
	"syscall"
)

var errPIDLockUnsupported = errors.New("pidfile locking is unsupported")

func lockPIDFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrAlreadyRunning
	}
	return err
}

func unlockPIDFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func pidFileLockHeld(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
}
```

`pidfile_lock_other.go`：

```go
//go:build !unix

package daemon

import (
	"errors"
	"os"
)

var errPIDLockUnsupported = errors.New("pidfile locking is unsupported")

func lockPIDFile(*os.File) error   { return errPIDLockUnsupported }
func unlockPIDFile(*os.File) error { return nil }
func pidFileLockHeld(*os.File) bool { return false }
```

`pidfile.go` 的 `DaemonAlive` 改为：

```go
	return pidFileLockHeld(f)
```

清理函数的 `syscall.Flock(..., LOCK_UN)` 改为 `unlockPIDFile(f)`。

- [ ] **Step 3: 验证两个平台**

Run:
```bash
go test ./internal/daemon -count=1
GOOS=windows GOARCH=amd64 go build ./...
```
Expected: 两者都成功。非 unix 上 `WritePidFile` 返回明确的 unsupported 错误，`DaemonAlive` 返回 false，不再编译失败。

- [ ] **Step 4: 提交**

```bash
git add internal/daemon/pidfile.go internal/daemon/pidfile_lock_unix.go internal/daemon/pidfile_lock_other.go
git commit -m "fix(daemon): make pidfile locking build on non-unix platforms"
```

---

### Task 10: CI 增加跨平台编译与全量 race

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/architecture.md`（如果 Task 9 选择了"非 unix 明确不支持"，在"Known limitations"中写明）

**Interfaces:**
- Consumes: 现有 `test` job。
- Produces: Windows 编译门禁；更宽的 race 覆盖。

- [ ] **Step 1: 确认基线**

Run: `GOOS=windows GOARCH=amd64 go build ./... && go test -race -count=1 ./internal/...`
Expected: 前者 PASS（Task 9 后），后者 PASS。若后者有已知不稳定的包，记录下来，只把稳定包加入 CI。

- [ ] **Step 2: 修改 CI**

在 `test` job 的 `Vet` 之后加：

```yaml
      - name: Cross-compile Windows
        run: GOOS=windows GOARCH=amd64 go build ./...
```

把 Race 步骤从：

```yaml
        run: go test -race -count=1 ./internal/app ./internal/session ./internal/transport/...
```

改为：

```yaml
        run: go test -race -count=1 ./internal/...
```

- [ ] **Step 3: 更新文档**

`docs/architecture.md` 的 Known limitations 加一条：

```markdown
- Windows builds are compiled in CI but daemon pidfile locking is
  unavailable there; `ggd` refuses to start with an explicit error instead
  of silently running without a single-instance guard.
```

同步 `docs/architecture.zh-CN.md`。

- [ ] **Step 4: 运行本地等价检查**

Run:
```bash
go vet ./...
go test -count=1 ./...
GOOS=windows GOARCH=amd64 go build ./...
```
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add .github/workflows/ci.yml docs/architecture.md docs/architecture.zh-CN.md
git commit -m "ci: gate Windows compilation and widen race coverage"
```

---

## 执行顺序与依赖

```
Task 1 → Task 2 → Task 3
Task 4 → Task 5
Task 6 → Task 7（可并行于 Task 4/5）
Task 8（独立）
Task 9 → Task 10（依赖 9 保证 windows build 通过）
```

Task 1/2 是安全修复，应最先合并。Task 5 依赖 Task 4 的锁实现。Task 7 只改组合根，可与其它任务并行。

## 不在本计划范围

- `internal/cliapp` 的 job/artifact/library/kb/memory 用例迁移到 `internal/app`（需要新增 JSON-RPC 方法或明确 CLI 仅本机执行的边界，另行制定计划）。
- `command_policy.go` 拆包为 `internal/shellpolicy`。
- UI `App.tsx` 拆分与协议版本常量同步。
