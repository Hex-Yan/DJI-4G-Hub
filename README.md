<p align="center">
  <img src="docs/images/DJI4GHub-logo.png" width="220" alt="DJI 4G Hub Logo">
</p>

<h1 align="center">DJI 4G Hub</h1>

<p align="center">
  适用于 DJI 第一代 Cellular Dongle 的 macOS 管理工具
</p>

<p align="center">
  <a href="https://github.com/Hex-Yan/DJI-4G-Hub/releases/latest">
    <img src="https://img.shields.io/github/v/release/Hex-Yan/DJI-4G-Hub?label=Release" alt="Latest Release">
  </a>
  <img src="https://img.shields.io/badge/macOS-Apple%20Silicon-black" alt="macOS Apple Silicon">
  <img src="https://img.shields.io/badge/USB-2CA3%3A4006-blue" alt="USB VID PID">
  <img src="https://img.shields.io/badge/license-PolyForm%20Noncommercial-lightgrey" alt="License">
</p>

<p align="center">
  <a href="https://github.com/Hex-Yan/DJI-4G-Hub/releases/latest"><strong>下载最新版本</strong></a>
  ·
  <a href="docs/USER_GUIDE.md">完整使用说明</a>
  ·
  <a href="https://github.com/Hex-Yan/DJI-4G-Hub/issues">问题反馈</a>
</p>

---

## 项目简介

DJI 4G Hub 是一款面向 macOS 的本地管理工具，用于控制和监测 DJI 第一代 Cellular Dongle。

它可以让模块长期连接在 Mac 上，同时提供 4G/LTE 网络、短信、来电监控、eSIM 管理、网络诊断和 AT 指令调试等功能。

从 v1.0.1 开始，安装程序会自动部署 USB Watcher。用户插入 DJI 4G 模块后，DJI 4G Hub 会自动启动，无需手动打开应用或执行终端命令。

> [!IMPORTANT]
> DJI 4G Hub 是非官方第三方项目，与 DJI、Quectel、运营商及 eSIM 卡片厂商不存在授权、隶属或合作关系。

## 主要功能

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 模块自动识别 | 已实现 | 识别 DJI 第一代 4G 模块，支持热插拔和重新连接 |
| 自动启动 App | 已实现 | 模块插入后由 USB Watcher 自动启动 DJI 4G Hub |
| 4G / LTE 网络 | 已实现 | USB 网卡接入、网络出口检测与流量监控 |
| 短信管理 | 已实现 | 接收、发送、自动轮询、验证码提取和旧短信清理 |
| 来电监控 | 已实现 | 显示来电状态、通话记录并支持拒接 |
| eSIM / eUICC | 已实现 | 读取、下载、启用、改名和删除兼容 Profile |
| 网络诊断 | 已实现 | USB 网络接口、默认出口、代理和连通性检查 |
| AT 调试 | 已实现 | 在界面中直接向模块发送 AT 指令 |
| 双向通话音频 | 尚未实现 | 当前只能监控和拒接，尚不能在 Mac 上接听通话 |
| Intel Mac | 尚未发布 | 当前 Release 仅提供 Apple Silicon 版本 |

## 界面预览

<p align="center">
  <img src="docs/images/macos-call-panel-redacted.png" width="46%" alt="Call Monitor">
  <img src="docs/images/macos-sms-panel-real-redacted.png" width="46%" alt="SMS Panel">
</p>

<p align="center">
  <img src="docs/images/network-traffic.png" width="46%" alt="Network Traffic">
  <img src="docs/images/esim-download.png" width="46%" alt="eSIM Profile">
</p>

## 下载与安装

前往：

[GitHub Releases](https://github.com/Hex-Yan/DJI-4G-Hub/releases/latest)

下载：

```text
DJI-4G-Hub-1.0.1.pkg
DJI-4G-Hub-1.0.1.pkg.sha256

