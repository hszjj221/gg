# 运行诊断与性能基线

[English](diagnostics.md) | 简体中文

CLI 和 daemon 共用输出到 stderr 的 JSON 日志。`GG_LOG_LEVEL=debug` 开启详细诊断；
支持 `debug`、`info`（默认）、`warn`（也接受 `warning`）和 `error`。

```bash
GG_LOG_LEVEL=debug gg -p "检查项目" 2>gg-debug.jsonl
GG_LOG_LEVEL=debug ggd --http 127.0.0.1:8765 --token development-token 2>ggd-debug.jsonl
```

默认级别下，daemon 记录运行开始、结束和运维告警。debug 增加会话、模型请求、
provider 尝试、审批和工具耗时。CLI 的 stdout 继续输出正常结果；嵌入调用方可以
通过 `app.Options.Log` / `cliapp.Options.Log` 指定日志处理器。

## 关联一次执行

| 字段 | 含义 |
| --- | --- |
| `sessionID`、`runID` | 对应会话和传输层运行；CLI 进程内调用生成运行 ID，关闭持久化时会话 ID 为空。 |
| `requestID`、`requestKind`、`model` | 运行内递增的请求序号，也覆盖共用 provider 的 subagent 请求；类型为 `turn` 或 `compaction`。 |
| `tool`、`toolCallID` | 关联工具准备、审批和执行。 |
| `durationMs`、`outcome` | 耗时和成功、失败、取消、超时；准备阶段另有 ready/skipped，审批可为 denied。 |
| `attempts`、`retries`、`compatibilityRetries` | provider 调用次数、实际执行的瞬时失败重试、协议兼容回退，分别计数；退避期间取消的重试可能只被安排而没有执行。 |
| `httpStatus`、`errorType`、`errorCode`、`retryable` | API 错误状态（无 API 错误时为 0）、Go 错误类型及稳定应用错误分类；详细错误仍返回调用方。 |
| `promptTokens`、`completionTokens`、`totalTokens` | 请求/工具用量及运行总用量，包含摘要压缩。 |

常规诊断省略提示词、思考内容、工具参数和结果、API key、远端错误正文；现有
运维告警与 panic 报告仍保留详细错误和堆栈。完成日志先于结果发布，日志写入在
Manager 锁外进行。

## 复现基准

```bash
go test -run '^$' -bench 'Benchmark(Snapshot|TreeProjection|ContextBuild)$' -benchmem -benchtime=150ms -count=3 ./internal/app ./internal/session ./internal/contextmgr
go test -run '^$' -bench '^BenchmarkSnapshot/records_10000$' -benchtime=600ms -cpuprofile /tmp/snapshot.cpu -memprofile /tmp/snapshot.heap -o /tmp/gg-app.test ./internal/app
go tool pprof -top -sample_index=alloc_space /tmp/gg-app.test /tmp/snapshot.heap
```

样本包含 100、1,000、10,000 条线性记录，文本大小为 256 字节；压缩后的上下文
保留 20 条消息和一段摘要。加载样本和写文件不计入耗时。这些数据测量核心投影
与上下文构建，不包含 JSON 传输序列化、网络/模型延迟、界面渲染或实际分支分布。

本机 Apple M4 / darwin arm64 / Go 1.27.1，10,000 条记录，三次测量中位数：

| 操作 | 优化前 | 优化后 | 每次分配字节，前 → 后 |
| --- | --- | --- | --- |
| 会话快照 | 9.75 ms | 3.95 ms | 41,202,864 → 10,477,225 |
| 会话树投影 | 5.20 ms | 3.51 ms | 12,987,787 → 7,908,779 |
| 全量历史上下文 | 2.04 ms | 2.02 ms | 3,695,104 → 3,695,104 |
| 压缩后上下文 | 71.2 µs | 4.21 µs | 1,691,712 → 8,256 |

分配采样确认快照主要开销来自 `Store.State` / `populateLoaded`。快照与会话更新
改为读取当前分支的 ID 和名称，省去全量历史恢复；会话树直接读取已验证的祖先
关系，省去路径复制；上下文按保留消息数量分配，已压缩历史不再占据预分配空间。
这些优化没有新增投影缓存或修改持久化格式。全量上下文开销仍随历史增长，快照
也仍包含完整可见会话树。

本机工具链缺少 `pprof` 可执行文件，本轮用工具链随附的 Go profile 解析代码读取
分配样本；生成的采样文件可在包含该工具的环境中用 `go tool pprof` 分析。
