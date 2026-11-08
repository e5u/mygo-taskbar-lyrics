# Taskbar Lyrics

在 Windows 任务栏上显示 MyGo Music 的当前歌词。支持逐字高亮、翻译、下一句预览和任务栏空位自动避让。

受 [TaskbarLyrics](https://github.com/ChissaQAQ/TaskbarLyrics) 启发。

## 安装

当前版本依赖 [e5u/mygo-music](https://github.com/e5u/mygo-music) 提供的 `host.nowPlaying()` 和后台插件支持。

1. 在 MyGo Music 中安装 `e5u/mygo-taskbar-lyrics`，或打开：

   `mygo-music://install-plugin?repo=e5u/mygo-taskbar-lyrics`

2. 在“设置 → 插件”中允许 Taskbar Lyrics 启动程序。
3. 播放音乐。

插件会随播放器启动，最小化播放器后仍可显示歌词。

## 功能

- 歌词逐字高亮，随播放进度平滑移动。
- 默认从 [AMLL TTML DB](https://github.com/amll-dev/amll-ttml-db) 匹配逐字时间；匹配期间或没有可靠结果时使用播放器原歌词。
- 支持翻译和下一句预览。
- 根据任务栏明暗自动选择文字颜色，也可设置固定颜色。
- 支持字体、字号、光晕强度、位置及偏移调整。
- 左右布局会扫描任务栏空位，尽量避开开始按钮、任务栏图标和托盘区域。
- 鼠标可穿透歌词，不影响任务栏操作。
- 暂停时可保留当前歌词，切歌时自动清除。

## 设置

| 设置 | 可选值 |
| --- | --- |
| AMLL 真实逐词 | `on` / `off` |
| 字体 | Windows 中已安装的字体名称 |
| 字号 | `small` / `default` / `large` |
| 摆放位置 | `left` / `center` / `right` |
| 颜色 | `auto` / `#RRGGBB` / `#AARRGGBB` |
| 光晕强度 | `0–200` |
| 译文 | `on` / `off` |
| 下一句 | `on` / `off` |
| 暂停时 | `on` / `off` |
| 横向微调 | 像素，支持负数 |
| 纵向微调 | 像素，支持负数 |

设置项也支持中文值。所有项目均有默认值，安装后无需额外配置。

## 工作原理

插件通过 `host.nowPlaying()` 获取歌曲、播放进度和歌词，再通过命名管道将状态发送给 Windows 原生渲染程序。渲染程序使用分层窗口覆盖在任务栏上，通过 GDI 绘制文字、逐字高亮和切换动画。

左右布局会读取任务栏图像以寻找空白区域；居中布局直接使用任务栏中央。窗口保持置顶，并随任务栏布局变化重新定位。

## 限制

- 仅支持 Windows 和主显示器任务栏。
- 没有时间信息的纯文本歌词不会显示。
- AMLL 匹配仅发送歌曲标题和歌手信息。
- 修改光晕强度后需要重启插件。

## 构建

```sh
go build -ldflags="-H=windowsgui" -o bin/taskbar-lyrics-windows-amd64.exe ./helper
```

使用 Windows GUI 子系统构建，避免运行时出现控制台窗口。
