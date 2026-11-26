# FastConf v1.7.0 可复现性能比较（2026-11-26）

本轮按约定将当前源码定义为 **FastConf v1.7.0**，测量启动、重载、热读、文件、环境变量和诊断成本。
表格由本轮原始数据计算，仅代表下述固定夹具、适配器与机器。

## 被测版本与方法

- FastConf v1.7.0 源码提交：`c75a3d301c4a26360ef016d6a3fc3e0c2634dc14`。
  比较模块通过 `replace github.com/fastabc/fastconf => ../..` 使用此本地源码。
  go.mod / modules.txt 中的 `v1.5.0` 是被替换的依赖声明，不是本轮实际被测版本。
- Apple M5 Pro，Darwin 27.0.0 / arm64，Go 1.26.11，GOMAXPROCS=4，GOGC=100，
  默认 encoding/json，未启用 jsonv2；计时没有 race 仪器。
- Viper 1.21.0、Koanf 2.3.0、Envconfig 1.4.0，仍使用独立模块固定的版本；未升级依赖。
  本轮移除了 Cleanenv：它间接依赖的 `olympos.io/encoding/edn` 在 `GOPROXY=direct` 下
  TLS 证书校验失败，比较模块无法 tidy。
- 61 组 benchmark，每组 6 次、benchtime=500ms，共 366 条样本。表格为全部六次的中位数，
  不删除离群值；benchstat 的置信区间和所有分配指标见原始 summary.txt。
  本轮多数项的置信区间在 ±1%–9%，纯环境变量启动的 FastConf 为 ±16%。
- 27 个独立进程各采集 100 条重载延迟，共 2700 条，分别保存 Darwin time -l 的峰值 RSS。
- 根模块 race 测试、比较适配器的 race 检查先通过，再串行运行跨库计时。
  计时期间未并行执行构建或测试；系统与其他应用仍可能产生调度噪声。
- 计时在干净工作区进行（environment.txt 中 `git diff --stat` 为空），运行时和 benchmark 源码未改动。

2026-11-08 报告（FastConf v1.6.0，源码 `d0364e0`，Go 1.26.10）保留在 Git 历史中。两轮的源码、
工具链和噪声不同，不能将两个日期的差值直接归因于 v1.7.0 的某项实现。
根模块基准与 JSON 实验对照另见[性能说明](../../docs/design/perf.md)。

## 夹具与工作边界

三种确定性 YAML 输入分别为 4096 / 65536 / 1048576 字节，32 / 512 / 8192 个列表项，
包含 Counter/Mirror 及填充字段。生成、文件写入在计时外。正确性检查验证各实现值相等、
有效更新发布新指针、相同输入不发布、Counter=0 的非法更新保留原指针，以及并发读取不混合快照。

FastConf 使用内存 Provider，故这些重载没有文件输入指纹的 shortcut。
Viper/Koanf 每次候选创建新解析器，并由应用适配器承担相同校验、JSON hash 去重与 atomic.Pointer 发布；
这些成本都包含在计时中。本夹具不包含未知字段；FastConf/Viper/Koanf 都检查未知字段，
语义边界见[方法说明](README.md)。

## 启动加载

内存 YAML → 新结构体 → 统一校验；FastConf 使用 Load，不启动后台 worker。单位：ms/op。

| 输入 | FastConf v1.7.0 | Viper | Koanf |
| --- | ---: | ---: | ---: |
| 4KiB | 0.1214 | 0.1003 | 0.1338 |
| 64KiB | 1.7292 | 1.5294 | 2.1058 |
| 1024KiB | 27.2951 | 25.1369 | 33.9806 |

## 运行时重载

有效更新交替改变 Counter/Mirror；相同值重载仍解析和校验；非法更新校验失败并保持原指针。
单位：ms/op。

| 输入 | 操作 | FastConf v1.7.0 | Viper + 发布适配器 | Koanf + 发布适配器 |
| --- | --- | ---: | ---: | ---: |
| 4KiB | 有效更新 | 0.1174 | 0.1056 | 0.1359 |
| 4KiB | 相同值 | 0.1171 | 0.1055 | 0.1389 |
| 4KiB | 校验拒绝 | 0.1136 | 0.1016 | 0.1370 |
| 64KiB | 有效更新 | 1.7206 | 1.6217 | 2.1941 |
| 64KiB | 相同值 | 1.6993 | 1.6667 | 2.1815 |
| 64KiB | 校验拒绝 | 1.6606 | 1.5765 | 2.0762 |
| 1024KiB | 有效更新 | 27.8197 | 25.8915 | 35.6596 |
| 1024KiB | 相同值 | 27.2438 | 27.4728 | 36.2053 |
| 1024KiB | 校验拒绝 | 25.7080 | 26.9580 | 34.3527 |

FastConf 在此夹具中并非启动/重载最快的实现：多数项慢于 Viper、快于 Koanf。1024KiB 相同值和
校验拒绝两项的中位数低于 Viper，但差值落在 benchstat 置信区间内，不能据此认定更快。
这些结果不包含远程网络，不能用本地表格推导云服务端到端时延。

## 热读与环境变量

| 热读路径 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Struct | 0.2450 | 0 | 0 |
| Atomic | 0.2759 | 0 | 0 |
| FastConf v1.7.0 | 0.4134 | 0 | 0 |
| Viper | 0.4774 | 0 | 0 |
| Koanf | 0.5188 | 0 | 0 |

Viper/Koanf 热读的是已经解码并原子发布的结构体，包含统一适配器调用，不是字符串键查询。
Struct 也代表静态 Envconfig 结果的字段读取。亚纳秒是紧循环吞吐量，不是业务请求延迟。

| 两个环境变量的启动解析 | µs/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| FastConf v1.7.0 | 9.7065 | 16,785 | 175 |
| Envconfig | 0.7457 | 552 | 28 |

纯环境变量启动下，FastConf 的来源装配及管线成本和分配均高于静态解析器；零分配热读不代表零成本加载。

## 文件启动与并发读

统一 os.ReadFile 后进入相同启动适配器；真实临时文件、热 OS 缓存，不含 watcher/网络。单位：ms/op。

| 输入 | FastConf v1.7.0 | Viper | Koanf |
| --- | ---: | ---: | ---: |
| 4KiB | 0.1552 | 0.1258 | 0.1623 |
| 64KiB | 1.8980 | 1.7347 | 2.2474 |
| 1024KiB | 29.3834 | 27.1671 | 34.7614 |

并发测试固定 4KiB、一个 1ms ticker 写者和并行读者，读者验证 Counter=Mirror。
下表是吞吐量，不是尾延迟；进程分配计数包含写者，显示的零是每次读取摊销并取整后的统计，
不代表写者不分配。ticker 的请求频率也不等于完成更新频率。

| 适配器 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| FastConf v1.7.0 | 0.1866 | 0 | 0 |
| Viper | 0.1500 | 0 | 0 |
| Koanf | 0.1502 | 0 | 0 |

## 独立重载延迟与峰值 RSS

以下为有效更新。先首次加载再 GC，无额外预热循环；nearest-rank p50/p95/p99 从每进程的
100 条原始延迟计算，不由 ns/op 推算。p99 只是第 99 个有序样本，不能作为服务 SLO。
Darwin 峰值 RSS 包含运行时、三份 fixture、解析器及加载状态，不是单快照大小或每操作分配量。

| 输入 | 实现 | p50 ms | p95 ms | p99 ms | 进程峰值 RSS MiB |
| --- | --- | ---: | ---: | ---: | ---: |
| 4KiB | FastConf v1.7.0 | 0.1112 | 0.2455 | 0.3217 | 18.22 |
| 4KiB | Viper | 0.1036 | 0.1518 | 0.2732 | 17.34 |
| 4KiB | Koanf | 0.1313 | 0.1741 | 0.2828 | 17.64 |
| 64KiB | FastConf v1.7.0 | 1.7422 | 2.2060 | 2.4328 | 20.16 |
| 64KiB | Viper | 1.7994 | 2.1687 | 2.3087 | 18.47 |
| 64KiB | Koanf | 2.1169 | 2.4805 | 2.6762 | 18.61 |
| 1024KiB | FastConf v1.7.0 | 28.0656 | 30.8903 | 32.3576 | 72.36 |
| 1024KiB | Viper | 27.8082 | 30.4202 | 33.5087 | 75.20 |
| 1024KiB | Koanf | 35.8641 | 38.9489 | 41.7909 | 73.95 |

相同值和非法重载的延迟/RSS 也保存于各自 JSON 与 memory.txt 文件中，未从采样中剔除。

## FastConf v1.7.0 诊断与历史成本

| 输入 | 操作 | ms/op | 分配 MiB/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| 4KiB | 已缓存视图的 Map 复制 | 0.0037 | 0.0111 | 68 |
| 4KiB | 有效重载 + history=8 + 冷 Map | 0.1601 | 0.1571 | 1,795 |
| 64KiB | 已缓存视图的 Map 复制 | 0.0583 | 0.1734 | 1,028 |
| 64KiB | 有效重载 + history=8 + 冷 Map | 2.2885 | 2.3236 | 25,877 |
| 1024KiB | 已缓存视图的 Map 复制 | 0.9498 | 2.7503 | 16,388 |
| 1024KiB | 有效重载 + history=8 + 冷 Map | 32.9144 | 38.0677 | 414,238 |

Map 返回调用者可修改的副本，warm Map 仍需复制。冷 Map + history 项包含完整重载，不能将其全部成本
归因于历史环；没有等价接口的竞品未参与此项。该夹具没有 secret 字段或 resolver；
secret 类型的 warm Map、计划构造、文件缓存等针对性结果见性能说明。
本轮亦未单独测量 `default` 标签、跨字段插值、托管 notifier、解密或 Schema 生成的额外成本。

## 复现与原始证据

从仓库根目录按顺序执行；各套计时不能并行：

```sh
export GOPROXY=direct GOWORK=off GOMAXPROCS=4 GOGC=100 GOEXPERIMENT=
make test
go -C benchmarks/compare test -mod=readonly -race -count=1 ./...
COUNT=6 BENCHTIME=500ms SAMPLES=100 \
  bash benchmarks/compare/run.sh results/2026-11-26-c75a3d3
```

比较证据位于本地 `results/2026-11-26-c75a3d3/`：environment.txt、modules.txt、correctness.txt、
source-sha256.txt、raw.txt、summary.txt，27 组延迟 JSON/RSS，以及本轮生成的 medians.json 和 latency-summary.json。
`source-sha256.txt` 记录文档更新前的源码快照；本轮运行时及基准 Go 文件保持与该提交一致。
结果目录被 Git 忽略，不随发布模块分发；克隆仓库后请用命令重新生成原始证据。

本轮使用 runner 固定的 benchstat 版本 `golang.org/x/perf@v0.0.0-20261009192801-be2c69fb417e`。
原始计时和采样期间没有升级依赖或切换 FastConf 源码。表格的四位小数只是显示精度，不是误差保证。
未测多层真实目录、远程服务、Kubernetes 投射卷、跨机器/平台性能或所有生产校验、订阅和密钥处理成本。
