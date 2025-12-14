# Video Compress 🎥

一个专为 Apple Silicon (M1/M2/M3) 芯片优化的极速视频压缩工具。利用 Go 语言的高并发特性与 macOS 的硬件加速引擎，实现视频体积的大幅缩减（平均 60%+），同时保持原画质分辨率。

## ✨ 特性

- **🚀 极致性能**: 针对 M-Series 芯片全链路优化，利用 VideoToolbox 硬件编解码。
- **⚡️ 高并发**: 自动利用双媒体引擎（Media Engines），支持并发批量处理。
- **📊 实时监控**: 基于全局时间流的精准进度条，告别“假死”状态。
- **🛠 零配置**: 智能预设，开箱即用，无需复杂的 FFmpeg 参数调优。
- **🛡 健壮性**: 优雅处理中断信号，支持断点清理。

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
make install

# 3. 验证
video-compress --help