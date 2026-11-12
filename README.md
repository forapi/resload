# resload

可调节 CPU 和内存占用的命令行程序。Go 标准库实现，无第三方模块、无 CGO、无网络服务；不修改系统设置。

从 [GitHub Releases](https://github.com/forapi/resload/releases/latest) 下载对应平台的程序，或下载完整包 `resload-v1.0.0.zip`。Git 仓库只保存源码和文档，可执行文件作为 Release 附件发布。

## 快速使用

以下示例按完整包解压后的 `bin/` 目录运行；单独下载可执行文件时，去掉路径中的 `bin/` 即可。Linux/macOS 下载后需要先赋予执行权限。

Windows x64，PowerShell：

```powershell
.\bin\resload-windows-amd64.exe --cpu 30% --memory 2GiB --duration 10m
```

Linux x64：

```sh
chmod +x ./bin/resload-linux-amd64
./bin/resload-linux-amd64 --cpu 30% --memory 2GiB --duration 10m
```

Apple Silicon Mac：

```sh
chmod +x ./bin/resload-darwin-arm64
./bin/resload-darwin-arm64 --cpu 30% --memory 2GiB --duration 10m
```

按实际系统和架构选择二进制文件。amd64 指 x64，arm64 指 64 位 ARM；darwin 指 macOS。

其他例子（以下省略可执行文件路径）：

```text
resload --cpu-cores 1.5 --memory 512MiB
resload --cpu 0% --memory 20% --duration 5m
resload --cpu 50% --memory 0 --duration 30s
resload --cpu-cores 0.25 --memory 64MiB --ramp 1s --duration 10s --json --no-stdin
```

Ctrl+C 退出。未指定时长时持续运行；正常退出、到期、收到中断或发生可处理错误时，停止工作线程并释放内存。

## CPU 占用口径

- `--cpu 30%`：当前有效 CPU 容量的 30%。8 个有效逻辑核心时，相当于约 2.4 个核心持续工作。
- `--cpu-cores 1.5`：约 1.5 个逻辑核心的算力。与 `--cpu` 互斥。
- 有效 CPU 容量取启动时系统可用逻辑核心数、进程亲和性、Linux cgroup 配额和 Go `GOMAXPROCS` 上限的较小值；允许小于 1 核的容器配额。
- 优先使用整核，再用一个线程产生剩余的部分核心负载。计算和休眠交替，默认周期 100ms，每秒根据整个进程的实际 CPU 时间反馈调节。
- 显示的 CPU 百分比按有效容量归一化。操作系统任务管理器可能采用宿主机总容量或单核为分母，显示值可能不同；`cpu_cores` 可用于直接比较。
- 调度竞争、CPU 限额和采样精度会造成短期波动；很低的目标下，监测和内存维护也有最低开销。该程序控制平均 CPU 时间，不承诺固定功耗或每个核心的相同占用。

## 内存占用口径

- `--memory 2GiB`：额外申请并写入 2GiB 内存，整个进程还有运行时开销。
- `--memory 25%`：有效内存总上限的 25%，不是“当前空闲内存”的 25%。Linux 容器优先取 cgroup 限制。
- `KiB/MiB/GiB/TiB` 按 1024 换算；`KB/MB/GB/TB` 按 1000 换算；不带单位表示字节。支持小数，例如 `1.5GiB`。
- 按系统页大小向上取整，以不超过 16MiB 的块分配。Windows 使用 VirtualAlloc/VirtualFree；Linux/macOS 使用 mmap/munmap。
- 初始化会写满申请的内存，填充不易压缩的伪随机数据；每 5 秒访问一次各页。`memory` 是已申请并触碰的量，`RSS` 是整个进程实际驻留内存。
- 默认保留 `64MiB` 可用内存余量。申请前检查余量，不足时报告错误并退出；可用 `--reserve 256MiB` 调大，或 `--reserve 0` 关闭此检查。检查无法排除其他进程同时申请内存导致的失败。
- 操作系统可能压缩或换出内存，因此 RSS 可以低于申请量；本程序不锁页。Linux 内存压力极高时，系统 OOM 机制仍可能直接结束进程。

## 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--cpu` | `0%` | 有效 CPU 总容量的目标比例，0–100% |
| `--cpu-cores` | 无 | 目标逻辑核心数，支持小数 |
| `--memory` | `0` | 额外内存大小或占有效上限的比例 |
| `--reserve` | `64MiB` | 分配时要求保留的可用内存余量 |
| `--duration` | `0` | 总运行时长，包含渐增阶段；0 表示不限时 |
| `--ramp` | `3s` | CPU 目标调整、内存增加的渐变时长；0 表示立即调整 |
| `--interval` | `1s` | CPU 反馈和状态输出间隔，至少 200ms |
| `--cpu-cycle` | `100ms` | CPU 计算/休眠周期，10ms–1s |
| `--json` | 关闭 | 输出逐行 JSON，适合日志或外部程序读取 |
| `--quiet` | 关闭 | 关闭状态输出，仍会反馈调节，错误写入 stderr |
| `--no-stdin` | 关闭 | 关闭运行时标准输入命令，适合后台运行 |
| `--version` / `--help` | — | 显示版本或帮助 |

启动时至少一个资源目标必须大于零。没有参数时打印错误；不会默认启动满负载。

## 运行时修改

在运行程序的终端输入命令并回车：

```text
cpu 20%
cpu-cores 1.5
memory 512MiB
memory 30%
memory 0
status
help
quit
```

CPU 目标会按 `--ramp` 渐变。内存增加按 `--ramp` 逐步分配，每 50ms 最多增加 16MiB，填充耗时或大目标可能延长实际达到目标的时间；减少内存会立即开始释放。可以把两个目标都改为 0，程序保持运行等待新命令。无效命令仅报告错误，保留原目标。stdin 关闭不会结束程序。

## 状态和 JSON

普通输出包含当前 CPU 比例、消耗核心数、CPU 目标、已申请内存、内存目标、RSS 和可用内存。

JSON 的 `type` 为 `start`、`status`、`target` 或 `stopped`。主要字段：

| 字段 | 含义 |
|---|---|
| `cpu_cores` / `cpu_percent` | 上一次反馈采样区间的实际进程 CPU 时间及归一化比例 |
| `target_cpu_cores` | 最终 CPU 目标，渐增阶段也显示最终值 |
| `memory_bytes` | 目前仍持有的额外内存量，按页取整 |
| `target_memory_bytes` | 用户请求的最终额外内存量 |
| `rss_bytes` | 最近一次采样的进程驻留内存 |
| `available_memory_bytes` | 可用内存估计，受 cgroup 剩余额度约束 |
| `available_cpu_cores` / `memory_limit_bytes` | 启动时用于解析百分比的有效资源上限 |
| `elapsed_seconds` / `pid` / `platform` | 运行时间、进程 ID、系统和架构 |
| `reason` | 停止原因，仅停止事件包含 |

正常停止事件在资源释放后输出，`memory_bytes` 为 0；CPU 字段保留最近一次反馈采样结果。运行时错误和无效命令写入 stderr。正常停止退出码为 0，参数或运行失败为 1。

## 编译与测试

需要 Go 1.22 或更新版本。

```sh
go test ./...
go vet ./...
go build -trimpath -o resload .
```

PowerShell 一次编译六个平台版本：

```powershell
.\build.ps1
```

脚本会先运行测试和静态检查，再生成 Windows、Linux、macOS 的 amd64/arm64 二进制。只重新打包时可用 `-SkipTests`。

手动交叉编译，例如 Linux ARM64：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o resload-linux-arm64 .
```

源码也可针对 Go 支持的其他 Linux CPU 架构编译；预编译包只包含上述六种常用组合。

## 平台范围与验证

- Windows：使用系统 API 读取进程 CPU 时间、工作集和可用物理内存，并识别进程 CPU 亲和性。未额外识别 Windows Job Object 的 CPU/内存配额；超过单处理器组的机器按 Go 运行时及可读取的亲和性容量计算。
- Linux：依赖 `/proc`，识别 cgroup v1/v2 当前组和可见祖先的 CPU、内存上限；CPU 亲和性取 `/proc/self/status`。隐藏在容器外的祖先限制无法读取。资源上限在启动时确定，调整容器限制后建议重启程序。
- macOS：CPU 时间、内存映射使用系统调用；内存总量、可用内存和当前 RSS 使用自带的 `/usr/sbin/sysctl`、`/usr/bin/vm_stat`、`/bin/ps`。可用内存按空闲、非活跃及推测页估算，不是系统内存压力指标。
- 实际运行验证平台为 Windows amd64。Linux/macOS 提供交叉编译检查；未在本机实际运行这些系统版本。具体检查结果见 `VALIDATION.md`。

源码按操作系统分文件，CPU 调度、参数解析、内存管理和命令处理共用。
