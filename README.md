# jse — JSON Schema TUI 配置编辑器

`jse` 读取一份 JSON Schema，生成 menuconfig 式的终端配置界面（单屏滚动列表：面包屑 + 条目 + 帮助栏），光标移动、就地编辑、层级钻取、当前路径向下搜索（标题/键/值），编辑即时写回；保存前用 [jsonschema](https://github.com/santhosh-tekuri/jsonschema) 库做权威校验。

构建：`go build -o jse ./cmd/jse`（Linux / macOS）。

## 用法

```
jse [-s <schema路径|http(s)://URL>] [--pick] [--force] [--accessible] <config.json>
```

示例：

```sh
# 编辑已有配置（原地写回）；文件不存在则从空文档开始、保存时创建
jse -s examples/server.schema.json server.json

# schema 地址写在配置顶层的 "$schema" 键里（无需 -s）
jse server.json

# 使用远端 schema
jse -s https://example.com/schemas/server.schema.json server.json
```

## Schema 来源（按顺序解析）

1. 显式 `-s`：`http://`/`https://` 开头 → 远端抓取；否则视为本地路径。
2. 无 `-s` → 读配置顶层 `$schema` 字符串：URL 直接用；无 scheme 的相对路径相对**配置文件所在目录**解析。
3. 两者皆无 → 查 SchemaStore 公共目录（schemastore.org）：按文件名做 `fileMatch` 匹配，**唯一命中直接采用**；歧义或未命中都打开检索选择器（`--pick` 强制打开）。选中即用，中止 → 报错 `no schema found: …`，exit 2。
4. 目录里也没有 → 报错 `no schema found: provide -s or set "$schema" in config`，exit 2。

选择器（TUI 全屏：输入过滤、↑/↓ 移动、enter 选定、esc 取消；`--accessible` 为行协议：输入搜索词 → 编号列表 → 数字选择，`0` 重搜、空行中止）。自动匹配或选定的 URL 按 `-s` 同样规则写回配置顶层 `$schema`，下次启动直接走第 2 优先级；`-pick` 则强制重选并覆盖既有 `$schema`。

schema 抓取失败（路径不存在 / HTTP 非 2xx / 超时）→ exit 1（远端 schema 有可用缓存时改为回退到缓存，见下节）。远端 `$ref`（包括相对 base URI、`$id`）同样支持；不可达的 ref 降级为原始 JSON 编辑（见下文）。

## 远端 schema 缓存

远端 schema（`http(s)://`）首次抓取后缓存到本地，之后的启动不再重复下载：

- **位置**：`$XDG_CACHE_HOME/jse`（通常是 `~/.cache/jse`），可用环境变量 `JSE_CACHE_DIR` 覆盖（例如 `/tmp/jse`）；目录不可写时静默降级为不缓存（不影响功能）。
- **有效期 24 小时**（`internal/fetch.CacheTTL`）：期内直接用缓存、**不发起任何请求**；过期后带 `If-None-Match`/`If-Modified-Since` 条件请求，`304` 继续用缓存并续期，内容变了才重新下载。
- **离线回退**：网络错误或 5xx 时用缓存启动，界面顶部提示 `⚠ using the cached schema: …`（无缓存则照旧报错 exit 1）；404 等客户端错误仍按失败处理，不会被缓存掩盖。
- **同一次运行只抓一次**：表单模型与校验器共用同一份字节；本地路径/`file://` 不缓存。
- **SchemaStore 目录同一套机制**：目录文档（catalog.json）与 schema 共用上述缓存/TTL/离线回退；环境变量 `JSE_CATALOG_URL` 覆盖目录地址（测试或内网镜像用）。
- 想强制取最新：删除缓存目录（`rm -rf ~/.cache/jse`）或换一个 `JSE_CACHE_DIR`。

## 界面与键位

```
Server Configuration
$
  Name*                   webapp
  Mode                    http
> Server*                 port=8080 host=0.0.0.0 +1 --->
  Logging                 — --->
  Mounts                  1 item --->
  Auth                    — --->
  < Save and exit >       
  < Edit raw JSON >       
↑/↓ · gg/G · enter edit/open · space · del delete · a add key · u undo · ctrl+r redo · esc · ctrl+s save · / search · r replace · ? help · ctrl+c quit
```

- 紧凑布局：标题行（当前菜单）+ **面包屑行**（文档路径 `$/a/b/0`：键与数组下标统一按 `/seg` 段；oneOf 拣选器/变体编辑器等不扩展路径的层级不占段；过宽时保留尾部并加 `…`）+ 行列表 + 底部操作提示栏（灰色 Faint，与主菜单区分）。`esc` 返回的层级保留光标位置。
- `-s` 的值会**自动填入文档顶层 `$schema` 字段**（文件已有非空值则保留原值、空值/缺失则填入并置于首位），保存后文件自带 schema 出处；下次可直接省略 `-s`（解析顺序见上）。schema 定义了 `$schema` 属性时（如 sing-box）该字段在编辑器中可见可改。
- 属性标记：红色 `*` = required（恒显，与该字段是否已填无关）。
- 变更标记：与**加载时/上次保存**的文档逐节点对比，值有变化的行在行尾显示粉色 `●`（对象/数组行在其子树含任何变更时也标记，父级逐层可见）；标题行右侧的 `●` 表示整个文档存在未保存变更；`ctrl+s` 保存成功后基线刷新、标记清除。中止退出（ctrl+c）本身不写文件；若此前 `ctrl+s` 已保存过，磁盘保留该次保存，其后的修改丢弃。
- schema 未定义的键（文档里有、schema 里没有）：作为**警告**（非错误）对待，在所属菜单中显示为黄色行，带 `(not in schema)` 与值摘要，行尾 `⚠` 也为黄色（校验错误保持红色）；`enter` 打开原始 JSON 编辑器修复（编辑器内提示），`delete` 直接删除；对象菜单中 `a` 可新增这样的键值对（输入键名 → `enter` → 原始 JSON 编辑器填值，`ctrl+s` 写入，`esc` 在任一步取消、不写入，空值 `ctrl+s` 视同取消）。这些键同时作为**编辑器级警告**进入问题列表（`unknown key … not in the schema`，黄色条目，与红色校验错误条目区分，enter 跳转定位到该行，删除即消）——JSON Schema 本身允许未知键，故不阻断保存，也不与 `additionalProperties: false` 的校验错误重复列出；顶层 `$schema`（编辑器自动写回的 schema 指针）不警告。搜索与替换同样覆盖这些键（见下）。
- 值配色（按类型）：字符串绿、数字青、布尔洋红、枚举琥珀、对象/数组加粗、未设 `—` 灰色、光标行整行反色高亮；`--->` 与 `●`/`[x]` 等标记用强调色。终端不支持颜色时自动降级为纯文本。
- 每行 = 字段标题 + 右侧值摘要：对象显示前 2 个成员 `k=v`（超出 `+N`；按 **schema 声明顺序**渲染，文档内键序不同也各行一致，schema 未定义的键殿后并保持文档顺序），数组 `N items`（**非纯对象列表**预览成员：`[4] a 1 {…} +1`——方括号为总数，列出前 3 个值（对象记 `{…}`、嵌套数组记 `[N]`），超出以 `+N` 收尾；纯对象数组保持 `N items`），oneOf 按**当前变体**给摘要——标量变体直接显示值（如 `tag` 的 string 变体显示 `geosite-github`）、枚举变体显示选项、数组变体同数组规则、布尔变体 `[y]`/`[n]`、对象变体有标题时保留 `变体标题 {…}` 以提供语境（无标题变体只显示 `{…}`，不合成 `variant N` 之类的无意义标签）；未设显示 `—`，无值时展示将生效的默认值。
- `↑`/`↓`/`j`/`k` 移动（到首行/末行后继续移动即**循环**；菜单列表、搜索、枚举选择器、问题/错误列表一致）；`gg`（两个 `g` 在一拍内）或 `Home` 跳到**列表第一行**，`G` 或 `End` 跳到**列表最后一行**——主菜单列表与问题列表、保存错误列表、枚举选择器 overlay 一致支持；搜索框中 `G`/`g` 是查询文本、`Home`/`End` 是行内光标键，不参与跳转；`←`/`h`、`→`/`l` 与方向键同义（枚举行循环切换、容器行钻取、`←`/`h` 返回上级；输入框中 `h`/`l` 仍是普通字符）；`enter`/`→`/`l` 编辑字段或钻取子菜单（对象/数组/oneOf 行尾带 `--->`）；`esc`/`q` 取消编辑、关 overlay 或返回上级（`q` 与 `esc` 在菜单/overlay 中**完全同义**；任何输入框内 `q` 是普通字符，取消一律用 `esc`；`←`/`h` 仅菜单内同义，输入框内是普通字符）（光标位置记忆）；根屏 `esc`/`q` 弹 `Quit without saving? (y/n)`，确认框 `y`/`enter`/`space` = 是、`n`/`esc` = 否。`delete` 删除选中的键值对（可选字段移除该键、必填字段拒绝并提示、数组条目行等同 `d`）；对象菜单 `a` 新增 schema 未定义的键值对（见上）。`?` 打开/关闭键位帮助 overlay（esc/`?`/`q` 关闭，列表模式可用）；光标所在行的 schema `description`（含模型附加的提示，如嵌套数组按 JSON 编辑）以灰色一行显示在底部提示栏上方，无描述的行不占行。
- `space` 切换布尔（`[y]`/`[n]`）与枚举数组成员（`[x]`）；枚举行 `←`/`→` 直接循环切换；标量字段 `enter` 就地输入（非法值红字拦截）；`json` 字段与根 `Edit raw JSON` 进 textarea（`ctrl+s` 应用）。
- 文本输入自动补全：候选 = 文档中**同键**已存在的字符串值（去重、按文档顺序、不含本处），按已输入前缀过滤（大小写不敏感）；带 `x-tag-reference` 的字符串字段（如 sing-box 的 `http_client`/`detour` 引用）改为按**引用域取 `tag` 值**（`http_client` → `http_clients[].tag`，兼容单复数两种键名，不限层级）作为候选。灰色提示行 `tab: a · b · c` 显示三个候选的**滚动窗口**（随 `tab` 选择前移，两侧 `…` 表示窗口外还有候选），`tab` 循环填入，`enter` 提交。无候选或无匹配时不显示提示行。
- 数组菜单：`< Add item >` 追加（对象条目进入条目表单，顶部 `< Remove this item >` 需确认 `Remove this item? (y/n)`）；条目行 `d` 直接删除（无确认，摘要即时可见）；`items` 为枚举的数组显示为多选 toggle 列表。
- oneOf 菜单：每变体一行（`●` 当前 + 变体签名；无标题变体不显示回退标签，仅有标记与签名）；**打开时光标落在当前值匹配的变体行**（`●` 行，未设值时在第一行）；切换已有值先确认 `Replace current value? (y/n)`。**标量变体**（如 `string`）：选中后**退回上一级菜单**，在字段行上就地编辑（输入框与补全提示都在该行，与其他字段一致），提交后 `●` 移至该变体；枚举变体进选项 overlay（保持在拣选器内），对象/数组变体进入子菜单。数组 `items` 为全对象 oneOf 时不选变体：`< Add item >` 直接进并集字段菜单，按已填字段（如判别 const）自动识别并收窄到对应变体，改判别键即可切换（菜单翻转后光标仍停在原字段行，例如 `type*`）。
- `/` 搜索**从当前面包屑路径向下**（子树范围，逐次打开重建索引）：匹配标题/键名**或当前值/概览**（默认大小写不敏感，`alt+i` 随时切换大小写敏感并实时重筛，叶子匹配值、容器匹配 `k=v` 概览，未设 `—` 不参与）；结果每行 `面包屑路径  概览`（路径格式与面包屑一致如 `$/server/port`，概览同主菜单值列，oneOf 变体行显示结构签名如 `{token*}`、不出现无意义的 `variant N` 回退标题，数组按文档实际条目逐条索引，schema 未定义的键连同其嵌套值一并索引），enter 跳转到对应菜单并定位该行，esc 关闭；overlay 底部有键位帮助行。结果排序——键名与查询完全一致（大小写按开关）的行置顶，其余保持文档顺序；查询中含 `/` 时按**路径**检索（只匹配 `$/a/b/0` 形式的路径，不再同时匹配标题/键名/值——找含 `/` 的值请去掉 `/` 用关键字）：前导 `/` 或 `$` 从文档根锚定（前缀匹配，允许最后一段未输完），否则在任意段边界子串匹配（如 `b/c` 命中 `$/a/b/c`）。
- **搜索替换（字符串值）**：列表模式 `r` 直接进入，或 `/` 打开后按 `tab` 挂出第二行替换输入框（`tab` 在搜索/替换框间切换焦点，esc 关闭）。替换模式的结果列表改为**字符串值**命中预览 `路径  旧值 → 新值`（数字/布尔/枚举/容器概览不参与，仅文档中实际存在的字符串——未设字段不替换默认值，schema 未定义键里的字符串同样可替换），范围与 `/` 相同。替换框内 `enter` 只替换当前高亮值（该值内所有出现处）并移到下一命中；`ctrl+r` 一次替换全部，overlay 关闭并提示 `replaced N occurrence(s) in M value(s)`。空查询 enter/`ctrl+r` 均不动作；大小写开关对搜索与替换统一生效。
- 宏录制/播放：`@` 开始录制（底栏右侧红色 `● REC n` 计数），再按 `@` 停止；`.` 回放最后一次宏。输入框（就地编辑/raw JSON/搜索）内 `@`/`.` 是普通字符、会被录下并在回放时照常键入；录制中 `@` 在任何非输入上下文（含 overlay）优先停止录制，非录制时 `@`/`.` 在所有非输入上下文（含 overlay，如问题列表）一律拦截——overlay 内也能起录/回放（问题列表改用 `esc`/`q` 关闭，宏内用 `esc` 关列表）。空缓冲停止 = 取消并保留上一条宏；录制中（非输入上下文）`.` 被忽略且不记录，回放不会嵌套触发；`q` 在非输入上下文与 `esc` 完全同义（见上），被录成 `esc`，输入框内则是普通字符、照常录制；**仅内存、单条宏**、上限 500 键（达到即停止并保留已录部分）。回放从当前界面状态立即重放，遇到中止（ctrl+c）或 `Save and exit` 即停止（提示停在第几键），`ctrl+s` 只保存、回放继续，落到不同菜单可能产生不同结果，由使用者负责。
- 撤销/重做：列表模式 `u` 撤销 / `ctrl+r` 重做；每次按键 = 一步（替换全部也是一步）；新修改清空重做链；上限 100 步；撤销/重做不改变上次保存基线（变更标记仍相对上次保存）；撤销后若当前菜单路径已不存在则退到最近仍存在的层级；输入框（就地编辑/raw JSON/搜索）内 `u` 为普通字符，搜索替换 overlay 内 `ctrl+r` 仍为全部替换；宏录制照常记录/回放这两个键。

`ctrl+s` 保存并**停留在编辑器**（成功提示 `Saved`，变更标记清零，可继续编辑后再存）；根菜单 `Save and exit` 保存并退出。校验错误全屏列出（`--force` 下 `y` 强存）。`ctrl+c` 任何时候退出（exit 130；此前 `ctrl+s` 已写盘的保留）。

## 保存与退出码

| 退出码 | 含义 | 结果行（stdout） |
|---|---|---|
| 0 | 已保存 | `Saved to <path>` |
| 1 | 运行错误（加载/解析失败等） | — |
| 2 | 用法错误（无 schema 来源、无输出路径） | — |
| 130 | 用户中止（此前未保存过 / 已 `ctrl+s` 保存过） | `Aborted, no changes written` / `Saved to <path>; aborted (changes after the last save are lost)` |

- 保存前用 schema 库做权威校验；有错误则列出并返回编辑，`--force` 可强存（结果行 `Saved with validation errors (--force): <path>`）。
- 写入为原子写（同目录临时文件 + rename），保留已存在文件的权限位；键序与数值字面量（如 `1.0`）原样保留；schema 之外的未知键、`$schema` 键均保留。
- `--accessible`：行式问答模式（无全屏 TUI，适合管道/屏幕阅读器）。注意：管道喂答案时脚本必须覆盖全部提问——required 输入框不能靠空行取默认值；stdin 读尽后会话中止。

## 支持的表单关键字子集

| 类别 | 关键字 | 表单行为 |
|---|---|---|
| 元信息 | `title` `description` `default` `examples[0]` | 标题/描述/预填/占位符 |
| 标量 | `type: string\|integer\|number\|boolean` | 文本输入 / 确认框 |
| 枚举 | `enum` / `const` | ≤5 选项行内单选，否则下拉 |
| 数值约束 | `minimum` `maximum` `exclusiveMinimum` `exclusiveMaximum` `multipleOf` | 输入校验 |
| 字符串约束 | `minLength` `maxLength` `pattern` | 输入校验 |
| 对象 | `properties` `required` | 字段按 schema 顺序；required 空值拦截 |
| 数组 | `items` `minItems` `maxItems` | items.enum→多选；标量条目→逐条输入；对象条目→逐条表单 |
| 组合 | `oneOf` `anyOf` | 变体选择后进入子编辑器；全 const/单值 enum 变体合并为一个枚举选择 |
| 组合 | `allOf` | 浅合并（properties 拼接、required 并集、约束取更严者） |
| 引用 | `$ref` | 本地指针与远端 ref 均支持（缓存 + 环保护，深度上限 32） |

## 降级规则

- 未列出的关键字不参与表单生成，但**保存时仍由 jsonschema 库权威校验**。
- 无法表单化的属性（不可达 `$ref`、数组套数组、未知类型）渲染为「Edit as JSON」文本域：提交时做 JSON 语法校验，语义合法性交给保存期校验。
- schema 编译失败（非法 schema / 远端 ref 不可达）→ 保存期校验自动停用，根屏状态行提示 `⚠ schema compile failed, save-time validation disabled`。
- 加载的配置若已有校验问题，不阻止编辑：根菜单首行显示问题计数 `enter` 打开问题列表，`↑/↓` 选择、`enter` 跳转到对应字段修改、`esc` 关闭；计数格式为 `⚠ N validation error(s)`、混合时 `⚠ N validation error(s) · M warning(s)`、仅警告时 `⚠ M warning(s)`；**问题列表实时刷新**——字段修好后计数、`⚠` 行标记与该条目即刻更新/消失（全部修完状态行自动隐藏，保存期校验仍是最终兜底）；**从问题列表跳转后，在落点菜单按 `esc` 会回到刷新过的问题列表**（光标位置保留），再 `esc` 关闭列表；若在落点继续下钻，`esc` 先逐级返回，回到落点层后再返回列表（`/` 搜索跳转或重新打开列表会清除该返回点）。出错字段行尾标 `⚠` 红色（其父级对象/数组行同样标记）；仅未知键警告的字段标黄色 `⚠`；问题列表条目同样按严重度着色（错误红、警告黄）。保存失败的错误列表同样支持 `enter` 跳转。
- 明确不做：`format` 校验、数组条目重排序 UI、`patternProperties`/`if-then-else`/`not` 的表单化、配置路径远端 URL。

## 开发

```sh
go build ./... && go vet ./... && go test ./...   # 单测 + e2e（accessible 行协议 + pty 冒烟）
go run ./cmd/jse -s examples/server.schema.json examples/server.config.json
```

包结构：`internal/jsondoc`（保序 JSON 文档）、`internal/fetch`（本地/远端统一加载）、`internal/catalog`（SchemaStore 目录加载与 fileMatch 匹配）、`internal/smodel`（schema→表单模型）、`internal/validate`（保存期校验）、`internal/ui`（menuconfig 菜单、schema 检索选择器与 --accessible huh 流程）、`cmd/jse`（CLI）。
