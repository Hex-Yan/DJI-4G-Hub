package main

import (
	"net/http"
	"strings"
	"time"
)

type ecmInitializationStatus struct {
	Connected           bool   `json:"connected"`
	Supported           bool   `json:"supported"`
	NeedsInitialization bool   `json:"needs_initialization"`
	Manufacturer        string `json:"manufacturer,omitempty"`
	Model               string `json:"model,omitempty"`
	Firmware            string `json:"firmware,omitempty"`
	USBNetMode          string `json:"usbnet_mode,omitempty"`
	USBCfg              string `json:"usbcfg,omitempty"`
	ATTransport         string `json:"at_transport,omitempty"`
	Reason              string `json:"reason,omitempty"`
}

func (a *app) ecmStatus(w http.ResponseWriter, _ *http.Request) {
	status := ecmInitializationStatus{}

	if a.currentUSBDevice() == nil {
		status.Reason = "未检测到 DJI USB 设备"
		writeJSON(w, http.StatusOK, status)
		return
	}
	status.Connected = true

	if err := a.ensureUSBAT(); err != nil {
		status.Reason = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}

	atiResponse, atiErr := a.runATCommand("ATI", 4*time.Second)
	usbnetResponse, usbnetErr := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	usbcfgResponse, usbcfgErr := a.runATCommand(`AT+QCFG="usbcfg"`, 4*time.Second)

	if atiErr != nil {
		status.Reason = "读取模块身份失败：" + atiErr.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}
	if usbnetErr != nil {
		status.Reason = "读取 usbnet 失败：" + usbnetErr.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}
	if usbcfgErr != nil {
		status.Reason = "读取 usbcfg 失败：" + usbcfgErr.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}

	identityUpper := strings.ToUpper(atiResponse)
	usbcfgUpper := strings.ToUpper(usbcfgResponse)

	if strings.Contains(identityUpper, "BAIWANG") {
		status.Manufacturer = "Baiwang"
	}
	if strings.Contains(identityUpper, "QDC507") {
		status.Model = "QDC507"
	}

	status.Firmware = parseUSBATFirmware(atiResponse)
	status.USBNetMode = parseUSBNetMode(usbnetResponse)
	status.USBCfg = parseUSBATPrefixed(usbcfgResponse, "+QCFG:")
	status.ATTransport = a.port

	status.Supported =
		status.Manufacturer == "Baiwang" &&
			status.Model == "QDC507" &&
			strings.Contains(usbcfgUpper, "0X2CA3") &&
			strings.Contains(usbcfgUpper, "0X4006")

	status.NeedsInitialization =
		status.Supported &&
			status.USBNetMode == "0"

	switch {
	case !status.Supported:
		status.Reason = "模块型号、固件身份或 USB 配置不在当前支持范围内"
	case status.USBNetMode == "1":
		status.Reason = "模块已经处于 ECM 模式"
	case status.USBNetMode == "0":
		status.Reason = "检测到原厂模式，可执行 ECM 初始化"
	case status.USBNetMode == "":
		status.Reason = "无法解析模块的 usbnet 模式"
	default:
		status.Reason = "检测到未验证的 usbnet 模式"
	}

	writeJSON(w, http.StatusOK, status)
}

type ecmInitializationResult struct {
	Accepted              bool   `json:"accepted"`
	AlreadyInitialized    bool   `json:"already_initialized"`
	PreviousUSBNetMode    string `json:"previous_usbnet_mode,omitempty"`
	ConfirmedUSBNetMode   string `json:"confirmed_usbnet_mode,omitempty"`
	ModuleRebootRequested bool   `json:"module_reboot_requested"`
	Message               string `json:"message"`
}

func (a *app) initializeECM(w http.ResponseWriter, _ *http.Request) {
	if a.currentUSBDevice() == nil {
		writeError(w, http.StatusNotFound, "未检测到 DJI USB 设备")
		return
	}

	if err := a.ensureUSBAT(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	atiResponse, err := a.runATCommand("ATI", 4*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取模块身份失败："+err.Error())
		return
	}

	usbcfgResponse, err := a.runATCommand(`AT+QCFG="usbcfg"`, 4*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取 USB 配置失败："+err.Error())
		return
	}

	identityUpper := strings.ToUpper(atiResponse)
	usbcfgUpper := strings.ToUpper(usbcfgResponse)

	supported :=
		strings.Contains(identityUpper, "BAIWANG") &&
			strings.Contains(identityUpper, "QDC507") &&
			strings.Contains(usbcfgUpper, "0X2CA3") &&
			strings.Contains(usbcfgUpper, "0X4006")

	if !supported {
		writeError(w, http.StatusUnprocessableEntity, "该模块不在当前 ECM 初始化支持范围内")
		return
	}

	currentResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取 usbnet 失败："+err.Error())
		return
	}

	currentMode := parseUSBNetMode(currentResponse)

	if currentMode == "1" {
		writeJSON(w, http.StatusOK, ecmInitializationResult{
			Accepted:            true,
			AlreadyInitialized:  true,
			PreviousUSBNetMode:  "1",
			ConfirmedUSBNetMode: "1",
			Message:             "模块已经处于 ECM 模式，无需重复初始化",
		})
		return
	}

	if currentMode != "0" {
		writeError(w, http.StatusConflict, "当前 usbnet 模式不是已验证的原厂模式 0，已停止初始化")
		return
	}

	setResponse, err := a.runATCommand(`AT+QCFG="usbnet",1`, 8*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "写入 ECM 模式失败："+err.Error())
		return
	}
	if !atCommandSucceeded(setResponse) {
		writeError(w, http.StatusBadGateway, "模块未确认 ECM 模式写入成功")
		return
	}

	confirmResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "复核 usbnet 失败："+err.Error())
		return
	}

	confirmedMode := parseUSBNetMode(confirmResponse)
	if confirmedMode != "1" {
		writeError(w, http.StatusBadGateway, "ECM 模式复核失败，模块未返回 usbnet=1")
		return
	}

	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 3*time.Second)
	if rebootErr != nil {
		a.markUSBATDetached("module reboot requested after ECM initialization")
	} else if !atCommandSucceeded(rebootResponse) {
		writeError(w, http.StatusBadGateway, "模块未确认重启命令")
		return
	}

	writeJSON(w, http.StatusAccepted, ecmInitializationResult{
		Accepted:              true,
		AlreadyInitialized:    false,
		PreviousUSBNetMode:    currentMode,
		ConfirmedUSBNetMode:   confirmedMode,
		ModuleRebootRequested: true,
		Message:               "ECM 模式已写入，模块正在重启并重新枚举",
	})
}
