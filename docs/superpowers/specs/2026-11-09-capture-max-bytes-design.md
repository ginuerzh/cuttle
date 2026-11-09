# 错误捕获 body 上限提升 + 管理台可配

Date: 2026-11-09
Status: design proposed

## Intent

错误排查时，capture 的 body 预览被截断：线上 `stats.capture.maxBytes` 是
16384（11-08 从默认 32768 降下来的），而请求体有 280–480KB、`tools` 在 JSON
末尾，所以预览里只看得到 system prompt。把上限提到 1MB，并让管理台能直接查看
和修改 capture 配置，不用改文件 + SIGHUP。

## Goals

- `stats.capture.maxBytes` 默认值 32768 -> 1048576（1MB，正好是现有校验上限，
  不需要改校验）。
- 管理台 Settings 页可查看和修改 capture 配置：`mode`、`sampleRate`、
  `maxBytes`、`retain`。
- 新增 `PUT /api/stats/capture`，只改 `stats.capture`，不触碰其他配置。
- 修改后新产生的捕获立即按新上限生效（沿用 reload -> `capture.SetPolicy`）。
- capture 增加**逐字段截断标记**，UI 在每个被截断的预览旁显示「已截断」。

## Non-goals

- 不做「头+尾」抓取（方案 B）。
- 不做结构化抽取（方案 C）。
- 不放宽 1MB 上限；超过 1MB 的 body 仍然截断。
- 不改 `capture.retain` 默认值（保持 24h）。
- 不把 provider `userAgent` / `headers` 做成可 round-trip，因此不做
  「全量 `PUT /api/config`」的 UI 入口。

## Approaches considered

### A. 加大上限 + 专用端点 + Settings 表单（chosen）

默认值提到 1MB；新增 `PUT /api/stats/capture`；Settings 页加一个 capture
配置区。

理由：错误排查要的是**原始完整报文**，可信度最高；这个部署里存储不是约束
（PVC 10Gi、DB 现在 19MB、一天 48 个可抓错误，1MB 下约 50MB/天，24h
retention）。改动集中在默认值 + 一个端点 + 一个表单。

代价：单次错误的存储上限变大（最坏 6 x maxBytes，实际远小于此）；需要新增
端点和 UI。

### B. 头+尾抓取

每个字段只存头部 N + 尾部 M，中间放截断标记。省空间，能用 64–128KB 看到
tools。但 JSON 不再连续、不能直接 parse；要改 capture 数据结构 + UI 展示；
被挖掉的中间部分可能正好是关键消息。

### C. 结构化抽取

解析 JSON，只保留 tools、最近 N 条消息、model。最省最聚焦，但
OpenAI / Anthropic / Gemini 各要一套抽取逻辑，脆弱，且丢失原始报文。

## Configuration

```yaml
stats:
  capture:
    mode: errors        # off | errors | sample
    sampleRate: 0.01
    maxBytes: 1048576   # 1024 .. 1048576
    retain: 24h
```

- `applyDefaults`：`maxBytes` 为空 -> 1048576；其余默认不变。
- `Validate`：沿用现有边界（1024..1048576）、`mode` 枚举、`sampleRate` 0..1、
  `retain` 非负。错误信息带 `stats.capture.` 前缀。
- 线上现有配置显式写着 `maxBytes: 16384`，需要单独改成 1048576（新默认只对
  新生成的配置生效）。

## API

- `PUT /api/stats/capture`，请求体 `{mode, sampleRate, maxBytes, retain}`，
  成功返回 204。
- 走 `mutate` / `replaceLocked`：clone -> 改 `Stats.Capture` ->
  `ApplyDefaults` -> `Validate` -> `Reload`（内部 `capture.SetPolicy`）->
  `config.Write`。
- 只替换 `stats.capture` 四个字段，其余字段保持原值。
- `GET /api/config` 已经返回 `stats.capture`，UI 直接读，不需要新 GET。

## Truncation marker

截断发生在 `clip` / `appendPreview` 把内容裁到 `maxBytes` 时。用**逐字段**标记，
而不是一个全局 bool：详情页每个预览是独立展示的，全局标记说不清是哪个字段被切。

- `capture.Capture` 增加 `TruncatedFields []string`
  （JSON `truncatedFields,omitempty`），元素是预览字段的 JSON 名：
  `requestOriginal`、`requestRewritten`、`responseOriginal`、
  `responseRewritten`、`sseFirstOriginal`、`sseFirstRewritten`、
  `sseLastOriginal`、`sseLastRewritten`。
- `clip` / `appendPreview` 返回 `(value, truncated bool)`；`Offer` 把被截断的
  字段记进 per-session 集合（同一字段多次 chunk 取或），`Finish` 输出排序后的
  切片。
- `request_captures` 增加 `truncated_fields TEXT NOT NULL DEFAULT ''`，由
  `ensureColumns` 迁移；`PutCapture` / `GetCapture` 负责 JSON 编解码，
  旧行读出空切片。
- Web `RequestCapture` 增加 `truncatedFields?: string[]`；`Preview` 增加
  `truncated` 属性，在标签旁渲染「已截断」Tag。

## Web

- `web/src/api/client.ts` 加 `putStatsCapture()`。
- `web/src/pages/Settings.tsx` 增加「错误捕获」区：
  - `mode`：Select（off / errors / sample）
  - `sampleRate`：InputNumber，仅在 sample 模式显示
  - `maxBytes`：InputNumber，步长 1024，提示 1KB–1MB
  - `retain`：Input，占位 `24h`
  - 保存后重新 `getConfig()` 刷新
- i18n 中英双份（`web/src/locales/en-US.ts`）。

## Testing

- `internal/config`：新默认值；边界（1023 / 1048577 拒绝）；非法 mode；
  空值回落默认。
- `internal/capture`：`appendPreview` / `clip` 的截断判定（恰好等于上限不算
  截断、chunk 超过剩余空间算）；同字段多次 chunk 只记一次；`Finish` 输出
  排序切片。
- `internal/stats`：`truncated_fields` 落库与读回，旧行为空。
- `internal/admin`：`PUT /api/stats/capture` 只改 capture；provider keys /
  userAgent / headers 不变；非法值 400；生效后新捕获按新上限。
- `web`：Vitest + `tsc --noEmit` + `npm run build`。
- 手动：把 `maxBytes` 设为 1MB，制造一次上游错误，确认请求预览包含 `tools`；
  再把 `maxBytes` 设为 1024，确认被截断的预览旁出现「已截断」。

## Acceptance criteria

- 默认 `maxBytes` = 1048576。
- Settings 页能查看并修改 capture 配置，保存后立即生效。
- 其他配置字段（尤其 provider keys / userAgent / headers）不受影响。
- 被截断的预览字段在详情页显示「已截断」；未截断的字段不显示。
- Go 与 web 全部门禁通过。
