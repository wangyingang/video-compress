# Video Compress (vc) 🎥

一个专为 Apple Silicon (M1/M2/M3) 芯片优化的极速视频压缩工具。利用 Go 语言的高并发特性与 macOS 的硬件加速引擎，实现视频体积的大幅缩减（平均 60%+），同时保持原画质分辨率。

## ✨ 特性

- **🚀 极致性能**: 针对 M-Series 芯片全链路优化，利用 VideoToolbox 硬件编解码。
- **⚡️ 高并发**: 自动利用双媒体引擎（Media Engines），支持并发批量处理。
- **📊 实时监控**: 基于全局时间流的精准进度条，告别“假死”状态。
- **🛠 零配置**: 智能预设，开箱即用，无需复杂的 FFmpeg 参数调优。
- **🛡 健壮性**: 支持随时中断并自动断点恢复，避免全量重跑。

## 📦 安装

### 前置要求
- macOS (Apple Silicon)
- FFmpeg (`brew install ffmpeg`)

### 编译安装
```bash
# 1. 克隆仓库
git clone [https://github.com/yourname/video-compress.git](https://github.com/yourname/video-compress.git)
cd video-compress

# 2. 编译并安装  
# 修改后，请运行 `make install`，系统将会编译生成名为 `vc` 的可执行文件，并将其移动到您的 `~/bin` 目录下。
make install

# 3. 验证
vc --help
```

## 🎮 使用方法

### 基础用法  
```bash
# 压缩单个文件
vc input.mp4

# 压缩整个目录
vc ./movies/
```

### 常用选项  
```bash
# 指定输出目录 (默认在原文件旁生成 *.compressed.mp4)
vc ./movies/ -o ./output/

# 使用高质量预设
vc input.mp4 -p high

# 自定义质量 (1-100，默认约 58)
vc input.mp4 -q 70

# 指定并发数 (默认 2)
vc ./movies/ -w 4

# 单文件分片续传片段时长 (默认 600 秒)
vc input.mp4 --segment-seconds 180

# 关闭单文件分片续传（退回整文件模式）
vc input.mp4 --disable-segment-resume
```

### 中断与恢复
```bash
# 运行中随时按 Ctrl+C 中断
# 再次执行同一命令后会自动恢复，只处理未完成文件
vc ./movies/
```

- 工具会将成功任务写入 checkpoint 文件 `.vc-resume.json`（位于输入目录或 `-o` 目录）。
- 编码先写入 `*.vcpart` 临时文件，完成后再原子重命名到最终输出；中断不会污染最终文件。
- 单文件默认启用“分片续传”：中断后会从未完成分片继续，而不是从 0% 重压整文件。

### 帮助  
```bash
vc --help
```
