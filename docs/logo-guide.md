# easyssh 品牌与 Logo 使用规范

本文档定义 easyssh 的 Logo 体系、配色与各场景用法,面向开源贡献者、文档作者与周边物料制作者。

## 1. Logo 概念

主图标由三个元素构成,对应产品核心能力:

| 元素 | 含义 |
|---|---|
| **盾牌** | 证书安全:私钥 0600、known_hosts 强制校验、加密通道 |
| **钥匙** | SSH 密钥/私钥:SSH 远程部署与密钥生命周期 |
| **开口钥匙环 + 旋转箭头** | 自动续期循环:30 天前续期、失败退避、SAN 变更重签 |

> 备选概念「终端 `>_` + 盾牌对勾」见 `variant-terminal.svg`,当前未采用,保留作演进参考。

## 2. 配色

| 色名 | 色值 | 用途 |
|---|---|---|
| 深空蓝 Dark | `#0A1F38` | 背景渐变终点、深色底 |
| 深海蓝 Mid | `#17456F` | 背景渐变起点、主色 |
| 终端绿 Bright | `#3DDC97` | 钥匙亮部、`ssh` 字样、强调 |
| 终端绿 Deep | `#1E9E5A` | 钥匙暗部、品牌绿 |
| 冷白 | `#F8FAFC` | 盾牌、文字(浅色版) |

色彩语义:深空蓝 = 信任/专业/技术;终端绿 = SSL/终端文化/续期成功。

## 3. 文件清单

全部源文件为矢量 SVG(可无限缩放),位图产物由 `logo-mark.svg` 渲染生成。

| 文件 | 场景 |
|---|---|
| `logo-mark.svg` | **主图标**:应用图标、favicon、GitHub 头像、社交头像 |
| `logo.svg` | **主标(浅色背景)**:文档、网站浅色区域、信纸 |
| `logo-dark.svg` | **主标(深色背景)**:深色 UI、深色背景展示 |
| `logo-mark-mono.svg` | **单色黑**:打印、印刷、水印、单色 UI |
| `logo-mark-white.svg` | **单色白**:深色背景水印、夜间模式 |
| `banner.svg` | **仓库横幅**(1500×500):GitHub README 顶部、仓库封面 |
| `favicon.ico` | 浏览器标签页(16/32/48/64 多尺寸内嵌) |
| `favicon-16/32/48/64.png` | 显式 favicon 引用、收藏夹 |
| `appicon-256/512.png` | 桌面应用图标(Wails GUI) |
| `variant-terminal.svg` | 备选概念(未采用) |
| `logo-preview.html` | 本地浏览器预览拼版 |

## 4. 使用规则

- **优先矢量**:任何新场景优先使用 SVG;位图仅用于无法使用 SVG 的平台(ICO/PNG)。
- **保持比例**:缩放时保持宽高比,禁止拉伸、变形、旋转或添加描边/投影。
- **安全间距**:图标四周至少保留图标宽度 1/8 的空白。
- **最小尺寸**:主标(带文字)最小高度 24px;纯图标最小 16px。
- **背景选择**:
  - 全彩图标(`logo-mark.svg`)自带深蓝底,直接用于浅色/深色背景均可。
  - 浅色背景用 `logo.svg`,深色背景用 `logo-dark.svg`,不要混用。
  - 单色场景:浅色底用 `logo-mark-mono.svg`,深色底用 `logo-mark-white.svg`。
- **禁用行为**:不得改动配色、不得与其它图形叠加、不得用于未获授权商品的商标主张。

## 5. 重新生成位图

源文件为 SVG,如需重新导出 PNG/ICO(例如调整后),用任意矢量工具(如
[Inkscape](https://inkscape.org)/[rsvg-convert](https://gitlab.gnome.org/GNOME/librsvg))
从 `assets/logo/logo-mark.svg` 导出即可;现有 ICO 内嵌 16/32/48/64 四档尺寸。

## 6. 落地位置

- GitHub 仓库设置 → 头像:上传 `logo-mark.svg`(或转 PNG)。
- README 顶部横幅:`banner.svg`。
- Wails GUI 图标:`cmd/easyssh-app/build/appicon.png`(已接入)。
- 前端浏览器 favicon:`cmd/easyssh-app/frontend/public/favicon.svg` + `favicon.ico`(已接入 `index.html`)。
