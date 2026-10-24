# 参数、数据格式与运行边界

## 命令

`doctor` 只检查 Python、相邻采集器与可选媒体依赖，不联网。`run` 默认执行有界的数据发现与筛选；`run --dry-run` 只预检条件配置。

数据筛选只用 Python 3.11+ 标准库，无需安装新的 SDK。目录需与 `bilibili-collect` 相邻，复用其 `metadata` 字段规则及按授权媒体采集；媒体依赖参见该 Skill。不开启 yt-dlp 自动搜索或无上限 playlist。

| 参数 | 作用与默认 |
| --- | --- |
| `--config PATH` | UTF-8 条件 JSON，最多128KiB；CLI同名条件覆盖配置 |
| `--keyword TEXT` | 可重复，最多10项，默认 all 字面匹配 |
| `--tag TEXT` | 可重复，最多10项，核实真实标签后默认 all 精确匹配 |
| `--keyword-match all/any` | 同时满足/任一关键词 |
| `--tag-match all/any` | 同时满足/任一标签 |
| `--keyword-fields ...` | title/description/tags，默认三者；简介仅暂用于筛选，不默认导出 |
| `--tid N` | 可重复的旧分区ID，最多10个，精确匹配详情tid |
| `--tid-v2 N` | 可重复的新版分区ID，最多10个，精确匹配详情tid_v2，不可和tid混用 |
| `--partition-name TEXT` | 可重复的准确分区名称，匹配tname或tname_v2，不自动展开子分区 |
| `--date-from YYYY-MM-DD` / `--date-to YYYY-MM-DD` | 必须成对，包含起止两个北京时间自然日，最长3661天 |
| `--last-days N` | 含今天的最近1–366天；没有日期时默认7天。与CLI固定日期互斥 |
| `--limit N` | 匹配条目上限，默认20，1–100 |
| `--max-pages N` | 所有查询合计页数，默认5，1–50 |
| `--max-candidates N` | 合计候选出现次数（含重复/已采集），默认100，1–1000 |
| `--max-requests N` | 首页匿名会话+数据API的请求预算，默认250，1–500，不含明确授权的媒体任务 |
| `--interval SECONDS` | 默认2，1–60；普通顺序请求，不并发、不自动重试 |
| `--timeout N` | 默认30，5–120秒，单次请求，不是整任务硬时限 |
| `--order pubdate/totalrank/click/stow` | 候选源排序，默认pubdate；不是全量全局排名承诺 |
| `--input PATH` | 本地JSON导入，默认不联网；最多16MiB/10000条 |
| `--output DIR` | 必须不存在的新运行目录，默认 `.artifacts/bilibili-discover/<UTC运行时间>` |
| `--state PATH` | 显式跨次BV去重，≤50000项，必须在单次运行目录外；不自动读现有工作区配置 |
| `--refresh` | 需同时指定state，重新输出已采集BV的当前数据；否则默认跳过已知BV |
| `--dry-run` | 不联网、不写文件、不读取Cookie/状态/候选；仅指定的条件配置会被读取 |
| `--cookies-file PATH` | 用户显式提供Netscape文件≤1MiB，仅保留Bilibili域，不改写原文件 |
| `--download` / `--subtitles` | 独立CLI授权后向旧采集器传入匹配BV第1P；不能由配置文件开启 |
| `--height N` / `--max-size-mib N` | 媒体衔接参数，默认720p/200MiB，遵循旧采集器限制 |

固定日期配置可以由CLI `--last-days` 整体替换，滚动日期配置可以由CLI成对的固定日期替换；同一次CLI不能同时给两种日期。每次回执保存解析后的精确日期和UTC+08:00时间戳边界。

## 配置字段

仅接受：`keywords,tags,tids,tid_v2s,partition_names,keyword_match,tag_match,keyword_fields,date_from,date_to,lookback_days,limit,max_pages,max_candidates,max_requests,interval_seconds,timeout,order`。

不接受 `cookies_file`、Cookie字面值、source_key、URL、输出/状态路径、download、脚本/exec、发布/定时等字段。不要在配置中保存密钥。示例 `examples/task.json` 是格式参考，不代表本轮已授权执行Python主题采集。

## 候选发现与复核

- 搜索候选：正常网页视频搜索路径 `/x/web-interface/search/type`，请求给关键词、排序、旧分区和日期提示。多个关键词all时以空格组合发出候选搜索，any时分开；tag-only用tag名作为搜索词。**服务端提示不是最终过滤依据**，必须逐条验证详情及标签。
- 无关键词/tag、只有旧tid时：正常分区列表 `/x/web-interface/newlist`。旧rid只作为候选源，最终详情tid精确匹配。父分区和子分区不是同一ID，不猜继承关系。
- 详情与标签：`/x/web-interface/view` 和 `/x/tag/archive/tags`。只构造固定Bilibili API主机与路径，不访问搜索结果给出的任意外站URL。
- 新tid_v2/分区名称只作为详情过滤条件；没有关键词/tag时需用户提供候选导出。不会把新编号塞进旧rid并声称已遍历新版分区。
- 请求只使用原站普通匿名首页Set-Cookie，或用户显式指定的正常Cookie。不生成buvid3、不生成WBI签名、不模拟浏览器登录、不使用签名/反爬绕过SDK。若普通请求被拒绝，则停止，不静默降级到其他镜像/账号/代理。
- 这些是可能变更或受限的网页接口，**不是官方对外开放API可用性保证**。原站签名/登录/风控变化会导致失败；离线合成验收不等于真实在线验收。若用户有官方授权数据渠道，应优先提供其导出文件走本地模式。
- 跨查询/分页结果可能重复或变化，排序也不能保证严格稳定；不按某一页的最老日期提前断言后续没有匹配。预算到达时保守报告截断。

## 导入格式

一个详情数组，或 `{ "videos": [ ... ] }`。每条至少：

```json
{
  "bvid": "BV0000000000",
  "title": "仅作格式演示的合成条目",
  "published_at": "2026-10-10T08:00:00+08:00",
  "owner": {"mid": 1, "name": "合成作者"},
  "tid": 201,
  "tname": "合成分区",
  "tags": ["Python", "教程"],
  "duration": 120,
  "stat": {"view": 100, "like": 10}
}
```

占位BV和分区编号不代表真实映射，不得向原站请求这些示例、不得作为真实资料入库。`pubdate` 可为真实Unix秒时间戳，也可以用带时区RFC3339的 `published_at`；不接受无时区日期作为精确发布时间。`tags` 可为名称字符串列表，或包含 `tag_name` 的对象列表；bgm项不当作视频tag。缺字段保持未知，不补造零值/作者/日期。导入仅按用户数据验证，标记为 `user_import`。

## 产物与去重

- `videos.json`：`{schema_version:1,videos:[...]}`，包括 BV、稳定ID、标题、UP主、公开mid、发布时间、旧/新分区字段、真实tag、整条投稿总时长/分P数、播放/点赞/投币/收藏/分享/评论/弹幕数快照及核验来源。无法取得的计数为null，不写成0；不抓取评论/弹幕正文或用户隐私。
- `items.json`：现有入库客户端支持的Content字段；截取可入库长度内的tag，保留真实来源URL和稳定ID；`summary` 明确只是视频信息，非内容摘要。并未提交到服务端。
- `videos.csv`：UTF-8 BOM，文本若以表格公式危险前缀起始会加单引号；JSON保留原始真实文本。
- `candidates.json`：逐条决策和拒绝/未验证原因，不含原始响应或简介全文。
- `manifest.json`：配置、预算、精确日期、数据来源模式、成功/失败、是否截断、媒体衔接与状态提交情况。`coverage=bounded_not_exhaustive` 永远不能解读为“该日期所有视频已采全”。
- `urls.txt` 是页面链接，不是时效播放地址。媒体下载默认不执行。

同一状态文件按BV全局去重，改变日期/关键词并不会另建去重命名空间；新主题请显式换状态文件。初次采集只保存一个统计快照，后续要更新热度数据需明确 `--refresh`。先仅数据采集、之后要下载相同BV时，也需要核对旧回执后显式 `--refresh` 或独立状态文件；否则报告 `media.status=not_run/reason=no_new_matches`，不会声称已下载。仅字幕模式不会启用视频下载。

只在成功/预算内正常完成且媒体也成功时原子写入状态，发现或媒体阶段失败不标记已完成。媒体部分失败时，完整文件会保留，但本批状态不提交；显式重跑新目录可能重复下载，需要先核对媒体逐条回执，不自动全批重跑。相同状态加锁，未授权清理未知锁，容量达到50000项不自动淘汰旧记录。若状态已提交后最终回执写失败，返回 `receipt_write_failed` 且保留真实 `state_committed` 和输出目录；旧磁盘manifest可能未更新，以返回值及状态文件核对，不盲目再执行。

## 自动执行与定时

脚本的run是单次自动分页、过滤和导出。可复用条件JSON配合 `--last-days` 和 `--state` 供用户明确授权的调度器调用；本 Skill 不自行注册计划任务，不修改系统服务或后台常驻设置。设置每天/每小时执行，必须另行确认频率、时区、运行机器、输出保留策略和授权范围；不能声称文件创建后已开始自动运行。

## 失败原则

输出目录已存在则拒绝覆盖。首次网络/权限/格式错误即停止，保留已核验数据，不打印上游原始异常、Cookie或响应正文，不自动重试。访问限制/验证码/非JSON不会触发账号轮换、Cookie伪造、签名逆向或代理切换。

`limit/max_pages/max_candidates/max_requests` 是预先约定的截断，不是全部失败；已核验匹配项可保存并提交去重状态，同时保留 `truncated=true`。真实异常或用户中止则 `ok=false` 且不提交状态。回执中未核验条目不能当作匹配。

## 测试

```powershell
python -X utf8 -m unittest discover -s skills/bilibili-discover/tests -v
python -X utf8 -m unittest discover -s skills/bilibili-collect/tests -v
python -X utf8 -m unittest discover -s skills/wzstudy-push/tests -v
```

所有新增测试应只使用合成数据和模拟响应，或本机回环测试；不读取真实Cookie/来源密钥，不访问随机真实视频，不写正式资料库。对真实原站的可访问性须在用户提供具体条件后单独验证。

## 技术参考与时效

已核对 yt-dlp 上游的 Bilibili 搜索/分区实现，复用本项目已有采集器的字段契约。没有复制其生成设备标识的搜索流程，也没有恢复已停止维护的第三方SDK。

- `https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/extractor/bilibili.py`
- 相邻 `../bilibili-collect/SKILL.md` 与 `../wzstudy-push/SKILL.md`（上述相对路径是从Skill目录出发；从本references目录需再上一级）

这些参考不是平台授权、接口稳定性、抓取完整性或媒体下载权的证明。
