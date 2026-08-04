package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// startAutoECMBootstrap periodically checks newly connected DJI QDC507
// modules and converts verified factory usbnet=0 devices to ECM usbnet=1.
func (a *app) startAutoECMBootstrap(ctx context.Context) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-timer.C:
			if err := a.autoInitializeECMOnce(); err != nil {
				log.Printf("automatic ECM bootstrap: %v", err)
			}
			timer.Reset(3 * time.Second)
		}
	}
}

func (a *app) autoInitializeECMOnce() error {
	if a.demo || a.modem != nil {
		return nil
	}

	a.ecmBootstrapMu.Lock()

	if a.ecmBootstrapInProgress {
		a.ecmBootstrapMu.Unlock()
		return nil
	}

	// Prevent a failed or rebooting module from being written repeatedly.
	if !a.ecmBootstrapLastAttempt.IsZero() &&
		time.Since(a.ecmBootstrapLastAttempt) < 20*time.Second {
		a.ecmBootstrapMu.Unlock()
		return nil
	}

	a.ecmBootstrapMu.Unlock()

	if a.currentUSBDevice() == nil {
		return nil
	}

	if err := a.ensureUSBAT(); err != nil {
		return err
	}

	atiResponse, err := a.runATCommand("ATI", 4*time.Second)
	if err != nil {
		return fmt.Errorf("read module identity: %w", err)
	}

	usbcfgResponse, err := a.runATCommand(`AT+QCFG="usbcfg"`, 4*time.Second)
	if err != nil {
		return fmt.Errorf("read USB configuration: %w", err)
	}

	identityUpper := strings.ToUpper(atiResponse)
	usbcfgUpper := strings.ToUpper(usbcfgResponse)

	supported :=
		strings.Contains(identityUpper, "BAIWANG") &&
			strings.Contains(identityUpper, "QDC507") &&
			strings.Contains(usbcfgUpper, "0X2CA3") &&
			strings.Contains(usbcfgUpper, "0X4006")

	if !supported {
		return nil
	}

	usbnetResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		return fmt.Errorf("read usbnet: %w", err)
	}

	currentMode := parseUSBNetMode(usbnetResponse)

	switch currentMode {
	case "1":
		a.ecmBootstrapMu.Lock()
		if a.ecmBootstrapLastResult != "ready" {
			log.Printf(
				"automatic ECM bootstrap: module already ready on %s",
				a.port,
			)
		}
		a.ecmBootstrapLastResult = "ready"
		a.ecmBootstrapMu.Unlock()
		return nil

	case "0":
		// This is the exact verified factory state that may be converted.
	default:
		return fmt.Errorf(
			"refusing to modify unverified usbnet mode %q",
			currentMode,
		)
	}

	a.ecmBootstrapMu.Lock()
	a.ecmBootstrapInProgress = true
	a.ecmBootstrapLastAttempt = time.Now()
	a.ecmBootstrapLastResult = "initializing"
	a.ecmBootstrapMu.Unlock()

	defer func() {
		a.ecmBootstrapMu.Lock()
		a.ecmBootstrapInProgress = false
		a.ecmBootstrapMu.Unlock()
	}()

	log.Printf(
		"automatic ECM bootstrap: verified factory Baiwang QDC507; "+
			"switching usbnet 0 -> 1 using %s",
		a.port,
	)

	setResponse, err := a.runATCommand(`AT+QCFG="usbnet",1`, 8*time.Second)
	if err != nil {
		a.setECMBootstrapResult("write_failed")
		return fmt.Errorf("write usbnet=1: %w", err)
	}
	if !atCommandSucceeded(setResponse) {
		a.setECMBootstrapResult("write_rejected")
		return errors.New("module did not confirm usbnet=1 write")
	}

	confirmResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapResult("verify_failed")
		return fmt.Errorf("verify usbnet=1: %w", err)
	}

	if parseUSBNetMode(confirmResponse) != "1" {
		a.setECMBootstrapResult("verify_mismatch")
		return errors.New("module did not report usbnet=1 after write")
	}

	log.Printf(
		"automatic ECM bootstrap: usbnet=1 confirmed; rebooting module",
	)

	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 3*time.Second)

	if rebootErr != nil {
		// Immediate USB loss after CFUN is normal for this firmware.
		log.Printf(
			"automatic ECM bootstrap: module disconnected during reboot: %v",
			rebootErr,
		)
	} else if !atCommandSucceeded(rebootResponse) {
		a.setECMBootstrapResult("reboot_rejected")
		return errors.New("module did not confirm reboot command")
	}

	a.setECMBootstrapResult("rebooting")

	// Close the old libusb handle. The normal discovery flow will open the
	// newly enumerated interface and verify usbnet=1 on the next cycle.
	a.markUSBATDetached("automatic ECM initialization requested module reboot")

	log.Printf(
		"automatic ECM bootstrap: waiting for USB re-enumeration",
	)

	return nil
}

func (a *app) setECMBootstrapResult(result string) {
	a.ecmBootstrapMu.Lock()
	a.ecmBootstrapLastResult = result
	a.ecmBootstrapMu.Unlock()
}
