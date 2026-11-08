# FastConf v1.6.0 可复现性能比较（2026-11-08）

本轮按约定将当前源码定义为 **FastConf v1.6.0**，测量启动、重载、热读、文件、环境变量和诊断成本。
表格由本轮原始数据计算，仅代表下述固定夹具、适配器与机器。

## 被测版本与方法

- FastConf v1.6.0 源码提交：`d0364e0cc7247463f34156723dfc2c59f8338615`。
  比较模块通过 `replace github.com/fastabc/fastconf => ../..` 使用此本地源码。
  go.mod / modules.txt 中的 `v1.5.0` 是被替换的依赖声明，不是本轮实际被测版本。
- Apple M5 Pro，Darwin 27.0.0 / arm64，Go 1.26.10，GOMAXPROCS=4，GOGC=100，
  默认 encoding/json，未启用 jsonv2；计时没有 race 仪器。
- Viper 1.21.0、Koanf 2.3.0、Cleanenv 1.5.0、Envconfig 1.4.0，仍使用独立模块固定的版本；未升级依赖。
- 68 组 benchmark，每组 6 次、benchtime=500ms，共 408 条样本。表格为全部六次的中位数，
  不删除离群值；benchstat 的置信区间和所有分配指标见原始 summary.txt。
- 27 个独立进程各采集 100 条重载延迟，共 2700 条，分别保存 Darwin time -l 的峰值 RSS。
- 根模块正确性测试、比较适配器的 race 检查先通过，再串行运行根模块、jsonv2 对照和跨库计时。
  本次任务未并行执行构建或 race 检查；系统与其他应用仍可能产生调度噪声。
- 计时期间运行时和 benchmark 源码未改动；本轮仅更新说明文档。

2026-10-25 历史报告保留在 Git 历史中；当时使用十次 1s 采样及不同源码快照，不能将两个日期的差值
直接归因于 v1.6.0 的某项实现。根模块 31 组和 JSON 实验对照另见[性能说明](../../docs/design/perf.md)。

## 夹具与工作边界

三种确定性 YAML 输入分别为 4096 / 65536 / 1048576 字节，32 / 512 / 8192 个列表项，
包含 Counter/Mirror 及填充字段。生成、文件写入在计时外。正确性检查验证各实现值相等、
有效更新发布新指针、相同输入不发布、Counter=0 的非法更新保留原指针，以及并发读取不混合快照。

FastConf 使用内存 Provider，故这些重载没有文件输入指纹的 shortcut。
Viper/Koanf 每次候选创建新解析器，并由应用适配器承担相同校验、JSON hash 去重与 atomic.Pointer 发布；
这些成本都包含在计时中。Cleanenv 使用 ParseYAML，环境变量另测。
本夹具不包含未知字段；FastConf/Viper/Koanf 检查未知字段，Cleanenv 此解析路径不检查，语义边界见[方法说明](README.md)。

## 启动加载

内存 YAML → 新结构体 → 统一校验；FastConf 使用 Load，不启动后台 worker。单位：ms/op。

| 输入 | FastConf v1.6.0 | Viper | Koanf | Cleanenv |
| --- | ---: | ---: | ---: | ---: |
| 4KiB | 0.1186 | 0.1012 | 0.1291 | 0.0691 |
| 64KiB | 1.6771 | 1.5475 | 2.0278 | 1.0469 |
| 1024KiB | 25.3834 | 23.4049 | 30.7077 | 16.2651 |

## 运行时重载

有效更新交替改变 Counter/Mirror；相同值重载仍解析和校验；非法更新校验失败并保持原指针。
单位：ms/op。

| 输入 | 操作 | FastConf v1.6.0 | Viper + 发布适配器 | Koanf + 发布适配器 |
| --- | --- | ---: | ---: | ---: |
| 4KiB | 有效更新 | 0.1171 | 0.1066 | 0.1394 |
| 4KiB | 相同值 | 0.1169 | 0.1043 | 0.1349 |
| 4KiB | 校验拒绝 | 0.1133 | 0.0989 | 0.1285 |
| 64KiB | 有效更新 | 1.6452 | 1.5454 | 1.9950 |
| 64KiB | 相同值 | 1.6537 | 1.5424 | 2.0274 |
| 64KiB | 校验拒绝 | 1.5568 | 1.4567 | 1.9428 |
| 1024KiB | 有效更新 | 25.3989 | 24.5033 | 31.9833 |
| 1024KiB | 相同值 | 25.3295 | 24.6753 | 31.8689 |
| 1024KiB | 校验拒绝 | 24.3046 | 23.4114 | 30.5532 |

FastConf 在此夹具中并非启动/重载最快的实现。这些结果不包含远程网络，不能用本地表格推导云服务端到端时延。

## 热读与环境变量

| 热读路径 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Struct | 0.2288 | 0 | 0 |
| Atomic | 0.3185 | 0 | 0 |
| FastConf v1.6.0 | 0.3900 | 0 | 0 |
| Viper | 0.4181 | 0 | 0 |
| Koanf | 0.4363 | 0 | 0 |

Viper/Koanf 热读的是已经解码并原子发布的结构体，包含统一适配器调用，不是字符串键查询。
Struct 也代表静态 Cleanenv/Envconfig 结果的字段读取。亚纳秒是紧循环吞吐量，不是业务请求延迟。

| 两个环境变量的启动解析 | µs/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| FastConf v1.6.0 | 8.1565 | 15,984 | 174 |
| Cleanenv | 0.6284 | 552 | 15 |
| Envconfig | 0.7611 | 552 | 28 |

纯环境变量启动下，FastConf 的来源装配及管线成本和分配均高于两个静态解析器；零分配热读不代表零成本加载。

## 文件启动与并发读

统一 os.ReadFile 后进入相同启动适配器；真实临时文件、热 OS 缓存，不含 watcher/网络。单位：ms/op。

| 输入 | FastConf v1.6.0 | Viper | Koanf | Cleanenv |
| --- | ---: | ---: | ---: | ---: |
| 4KiB | 0.1286 | 0.1084 | 0.1376 | 0.0770 |
| 64KiB | 1.6138 | 1.5134 | 1.9892 | 1.0687 |
| 1024KiB | 27.3033 | 25.0881 | 32.4103 | 17.0369 |

并发测试固定 4KiB、一个 1ms ticker 写者和并行读者，读者验证 Counter=Mirror。
下表是吞吐量，不是尾延迟；进程分配计数包含写者，显示的零是每次读取摊销并取整后的统计，
不代表写者不分配。ticker 的请求频率也不等于完成更新频率。

| 适配器 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| FastConf v1.6.0 | 0.1421 | 0 | 0 |
| Viper | 0.1229 | 0 | 0 |
| Koanf | 0.1218 | 0 | 0 |

## 独立重载延迟与峰值 RSS

以下为有效更新。先首次加载再 GC，无额外预热循环；nearest-rank p50/p95/p99 从每进程的
100 条原始延迟计算，不由 ns/op 推算。p99 只是第 99 个有序样本，不能作为服务 SLO。
Darwin 峰值 RSS 包含运行时、三份 fixture、解析器及加载状态，不是单快照大小或每操作分配量。

| 输入 | 实现 | p50 ms | p95 ms | p99 ms | 进程峰值 RSS MiB |
| --- | --- | ---: | ---: | ---: | ---: |
| 4KiB | FastConf v1.6.0 | 0.1250 | 0.1587 | 0.2780 | 17.48 |
| 4KiB | Viper | 0.0968 | 0.1143 | 0.2515 | 16.98 |
| 4KiB | Koanf | 0.1294 | 0.1622 | 0.2687 | 16.42 |
| 64KiB | FastConf v1.6.0 | 1.6168 | 1.8007 | 2.0209 | 18.86 |
| 64KiB | Viper | 1.5964 | 1.8061 | 1.9386 | 18.44 |
| 64KiB | Koanf | 2.0101 | 2.1241 | 2.2720 | 19.17 |
| 1024KiB | FastConf v1.6.0 | 25.6220 | 27.1768 | 28.3541 | 77.59 |
| 1024KiB | Viper | 24.6206 | 25.8776 | 26.9773 | 82.08 |
| 1024KiB | Koanf | 31.9645 | 34.1925 | 35.1499 | 81.42 |

相同值和非法重载的延迟/RSS 也保存于各自 JSON 与 memory.txt 文件中，未从采样中剔除。

## FastConf v1.6.0 诊断与历史成本

| 输入 | 操作 | ms/op | 分配 MiB/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| 4KiB | 已缓存视图的 Map 复制 | 0.0031 | 0.0111 | 68 |
| 4KiB | 有效重载 + history=8 + 冷 Map | 0.1437 | 0.1569 | 1,787 |
| 64KiB | 已缓存视图的 Map 复制 | 0.0506 | 0.1734 | 1,028 |
| 64KiB | 有效重载 + history=8 + 冷 Map | 2.0207 | 2.3134 | 25,868 |
| 1024KiB | 已缓存视图的 Map 复制 | 0.8045 | 2.7503 | 16,388 |
| 1024KiB | 有效重载 + history=8 + 冷 Map | 30.7046 | 37.8829 | 414,146 |

Map 返回调用者可修改的副本，warm Map 仍需复制。冷 Map + history 项包含完整重载，不能将其全部成本
归因于历史环；没有等价接口的竞品未参与此项。该夹具没有 secret 字段或 resolver；
secret 类型的 warm Map、计划构造、文件缓存等针对性结果见性能说明。
本轮亦未单独测量 `default` 标签、跨字段插值、托管 notifier、解密或 Schema 生成的额外成本。

## 复现与原始证据

从仓库根目录按顺序执行；各套计时不能并行：

```sh
export GOPROXY=direct GOWORK=off GOMAXPROCS=4 GOGC=100 GOEXPERIMENT=
go test -mod=readonly -count=1 ./...
go -C benchmarks/compare test -mod=readonly -race -count=1 ./...
go test -mod=readonly -run '^$' -bench . -benchmem -benchtime=500ms -count=6 ./...
GOEXPERIMENT=jsonv2 go test -mod=readonly -run '^$' \
  -bench '^BenchmarkReloadWide$' -benchmem -benchtime=500ms -count=6 .
bash tools/bench-guard.sh
COUNT=6 BENCHTIME=500ms SAMPLES=100 \
  bash benchmarks/compare/run.sh results/2026-11-08-d0364e0
```

根模块证据位于 `/tmp/fastconf-benchmarks-20261108-d0364e0/`。
比较证据位于本地 `results/2026-11-08-d0364e0/`：environment.txt、modules.txt、correctness.txt、
source-sha256.txt、raw.txt、summary.txt，27 组延迟 JSON/RSS，以及本轮生成的 medians.json 和 latency-summary.json。
`source-sha256.txt` 记录文档更新前的源码快照；本轮运行时及基准 Go 文件保持与该提交一致。
结果目录被 Git 忽略，不随发布模块分发；克隆仓库后请用命令重新生成原始证据。

本轮使用 runner 固定的 benchstat 版本 `golang.org/x/perf@v0.0.0-20261009192801-be2c69fb417e`。
原始计时和采样期间没有升级依赖或切换 FastConf 源码。表格的四位小数只是显示精度，不是误差保证。
未测多层真实目录、远程服务、Kubernetes 投射卷、跨机器/平台性能或所有生产校验、订阅和密钥处理成本。
