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

const (
	moduleInitDisconnected = "disconnected"
	moduleInitDetected     = "detected"
	moduleInitChecking     = "checking"
	moduleInitInitializing = "initializing"
	moduleInitRebooting    = "rebooting"
	moduleInitReady        = "ready"
	moduleInitFailed       = "failed"
)

const (
	supportedQDC507USBCfg = "0x2ca3,0x4006,1,1,1,1,1,0,0"
	ecmRebootWaitWindow   = 45 * time.Second
	ecmFailedRetryWindow  = 30 * time.Second
)

type ecmInitializationStatus struct {
	Connected             bool   `json:"connected"`
	Supported             bool   `json:"supported"`
	NeedsInitialization   bool   `json:"needs_initialization"`
	Manufacturer          string `json:"manufacturer,omitempty"`
	Model                 string `json:"model,omitempty"`
	Firmware              string `json:"firmware,omitempty"`
	USBNetMode            string `json:"usbnet_mode,omitempty"`
	USBCfg                string `json:"usbcfg,omitempty"`
	ATTransport           string `json:"at_transport,omitempty"`
	InitializationStatus  string `json:"initialization_status"`
	InitializationMessage string `json:"initialization_message,omitempty"`
	InitializationError   string `json:"initialization_error,omitempty"`
	InitializationBusy    bool   `json:"initialization_busy"`
	Reason                string `json:"reason,omitempty"`
}

func (a *app) ecmStatus(w http.ResponseWriter, _ *http.Request) {
	status := ecmInitializationStatus{
		InitializationStatus: moduleInitDisconnected,
	}
	snapshot := a.currentECMBootstrapState()
	if snapshot.Status != "" {
		status.InitializationStatus = snapshot.Status
		status.InitializationMessage = snapshot.Message
		status.InitializationError = snapshot.Error
		status.InitializationBusy = snapshot.InProgress
	}

	if a.currentUSBDevice() == nil {
		if status.InitializationStatus == moduleInitRebooting &&
			time.Since(snapshot.LastAttempt) <= ecmRebootWaitWindow {
			status.Reason = "正在等待模块重启"
			writeJSON(w, http.StatusOK, status)
			return
		}
		a.setECMBootstrapState(moduleInitDisconnected, "未检测到 DJI USB 设备", "")
		status.InitializationStatus = moduleInitDisconnected
		status.InitializationMessage = "未检测到 DJI USB 设备"
		status.InitializationError = ""
		status.InitializationBusy = false
		status.Reason = "未检测到 DJI USB 设备"
		writeJSON(w, http.StatusOK, status)
		return
	}
	status.Connected = true
	if status.InitializationStatus == moduleInitDisconnected {
		status.InitializationStatus = moduleInitDetected
		status.InitializationMessage = "发现新的 DJI 第一代 4G 模块"
	}

	if snapshot.InProgress &&
		(status.InitializationStatus == moduleInitInitializing ||
			status.InitializationStatus == moduleInitRebooting) {
		status.Reason = firstNonEmpty(snapshot.Message, "模块初始化正在进行")
		writeJSON(w, http.StatusOK, status)
		return
	}

	if err := a.ensureUSBAT(); err != nil {
		a.setECMBootstrapState(moduleInitDetected, "发现新的 DJI 第一代 4G 模块", err.Error())
		status.InitializationStatus = moduleInitDetected
		status.InitializationMessage = "发现新的 DJI 第一代 4G 模块"
		status.InitializationError = err.Error()
		status.InitializationBusy = false
		status.Reason = err.Error()
		writeJSON(w, http.StatusOK, status)
		return
	}

	if snapshot.Status != moduleInitReady && snapshot.Status != moduleInitChecking {
		a.setECMBootstrapState(moduleInitChecking, "正在检查模块 ECM 状态", "")
	}
	atiResponse, atiErr := a.runATCommand("ATI", 4*time.Second)
	usbnetResponse, usbnetErr := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	usbcfgResponse, usbcfgErr := a.runATCommand(`AT+QCFG="usbcfg"`, 4*time.Second)

	if atiErr != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取模块身份失败："+atiErr.Error())
		status.Reason = "读取模块身份失败：" + atiErr.Error()
		status.InitializationStatus = moduleInitFailed
		status.InitializationMessage = "初始化失败"
		status.InitializationError = status.Reason
		status.InitializationBusy = false
		writeJSON(w, http.StatusOK, status)
		return
	}
	if usbnetErr != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取 usbnet 失败："+usbnetErr.Error())
		status.Reason = "读取 usbnet 失败：" + usbnetErr.Error()
		status.InitializationStatus = moduleInitFailed
		status.InitializationMessage = "初始化失败"
		status.InitializationError = status.Reason
		status.InitializationBusy = false
		writeJSON(w, http.StatusOK, status)
		return
	}
	if usbcfgErr != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取 usbcfg 失败："+usbcfgErr.Error())
		status.Reason = "读取 usbcfg 失败：" + usbcfgErr.Error()
		status.InitializationStatus = moduleInitFailed
		status.InitializationMessage = "初始化失败"
		status.InitializationError = status.Reason
		status.InitializationBusy = false
		writeJSON(w, http.StatusOK, status)
		return
	}

	identity := classifyQDC507ECMStatus(atiResponse, usbcfgResponse, usbnetResponse)

	applyECMIdentityToStatus(&status, identity, a.port)
	a.setECMBootstrapState(status.InitializationStatus, status.InitializationMessage, status.InitializationError)

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
	if !a.beginECMBootstrap(moduleInitChecking, "正在检查模块 ECM 状态", false) {
		writeError(w, http.StatusConflict, "模块初始化正在进行")
		return
	}
	defer a.endECMBootstrap()

	if a.currentUSBDevice() == nil {
		a.setECMBootstrapState(moduleInitDisconnected, "未检测到 DJI USB 设备", "")
		writeError(w, http.StatusNotFound, "未检测到 DJI USB 设备")
		return
	}

	if err := a.ensureUSBAT(); err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	atiResponse, err := a.runATCommand("ATI", 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取模块身份失败："+err.Error())
		writeError(w, http.StatusBadGateway, "读取模块身份失败："+err.Error())
		return
	}

	usbcfgResponse, err := a.runATCommand(`AT+QCFG="usbcfg"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取 USB 配置失败："+err.Error())
		writeError(w, http.StatusBadGateway, "读取 USB 配置失败："+err.Error())
		return
	}

	identity := classifyQDC507ECMStatus(atiResponse, usbcfgResponse, "")
	if !identity.Supported {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "该模块不在当前 ECM 初始化支持范围内")
		writeError(w, http.StatusUnprocessableEntity, "该模块不在当前 ECM 初始化支持范围内")
		return
	}

	currentResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取 usbnet 失败："+err.Error())
		writeError(w, http.StatusBadGateway, "读取 usbnet 失败："+err.Error())
		return
	}

	currentMode := parseUSBNetMode(currentResponse)

	if currentMode == "1" {
		a.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
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
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "当前 usbnet 模式不是已验证的原厂模式 0，已停止初始化")
		writeError(w, http.StatusConflict, "当前 usbnet 模式不是已验证的原厂模式 0，已停止初始化")
		return
	}

	a.recordECMBootstrapAttempt()
	a.setECMBootstrapState(moduleInitInitializing, "正在启用 ECM", "")
	setResponse, err := a.runATCommand(`AT+QCFG="usbnet",1`, 8*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "写入 ECM 模式失败："+err.Error())
		writeError(w, http.StatusBadGateway, "写入 ECM 模式失败："+err.Error())
		return
	}
	if !atCommandSucceeded(setResponse) {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "模块未确认 ECM 模式写入成功")
		writeError(w, http.StatusBadGateway, "模块未确认 ECM 模式写入成功")
		return
	}

	confirmResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "复核 usbnet 失败："+err.Error())
		writeError(w, http.StatusBadGateway, "复核 usbnet 失败："+err.Error())
		return
	}

	confirmedMode := parseUSBNetMode(confirmResponse)
	if confirmedMode != "1" {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "ECM 模式复核失败，模块未返回 usbnet=1")
		writeError(w, http.StatusBadGateway, "ECM 模式复核失败，模块未返回 usbnet=1")
		return
	}

	a.setECMBootstrapState(moduleInitRebooting, "正在等待模块重启", "")
	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 3*time.Second)
	if rebootErr != nil {
		a.markUSBATDetached("module reboot requested after ECM initialization")
	} else if !atCommandSucceeded(rebootResponse) {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "模块未确认重启命令")
		writeError(w, http.StatusBadGateway, "模块未确认重启命令")
		return
	} else {
		a.markUSBATDetached("module reboot requested after ECM initialization")
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
			_ = a.autoInitializeECMOnce()
			timer.Reset(3 * time.Second)
		}
	}
}

func (a *app) autoInitializeECMOnce() error {
	if a.demo || a.modem != nil {
		return nil
	}

	if a.currentUSBDevice() == nil {
		state := a.currentECMBootstrapState()
		if state.Status == moduleInitRebooting && time.Since(state.LastAttempt) <= ecmRebootWaitWindow {
			return nil
		}
		a.setECMBootstrapState(moduleInitDisconnected, "未检测到 DJI USB 设备", "")
		return nil
	}

	if !a.beginECMBootstrap(moduleInitChecking, "正在检查模块 ECM 状态", true) {
		return nil
	}
	defer a.endECMBootstrap()

	if err := a.ensureUSBAT(); err != nil {
		a.setECMBootstrapState(moduleInitDetected, "发现新的 DJI 第一代 4G 模块", err.Error())
		return err
	}

	atiResponse, err := a.runATCommand("ATI", 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取模块身份失败："+err.Error())
		return fmt.Errorf("read module identity: %w", err)
	}

	usbcfgResponse, err := a.runATCommand(`AT+QCFG="usbcfg"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取 USB 配置失败："+err.Error())
		return fmt.Errorf("read USB configuration: %w", err)
	}

	identity := classifyQDC507ECMStatus(atiResponse, usbcfgResponse, "")
	if !identity.Supported {
		a.setECMBootstrapState(moduleInitDetected, "发现新的 DJI 第一代 4G 模块", "模块型号、固件身份或 USB 配置不在当前支持范围内")
		return nil
	}

	usbnetResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "读取 usbnet 失败："+err.Error())
		return fmt.Errorf("read usbnet: %w", err)
	}

	currentMode := parseUSBNetMode(usbnetResponse)

	switch currentMode {
	case "1":
		a.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
		return nil

	case "0":
		// This is the exact verified factory state that may be converted.
	default:
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "检测到未验证的 usbnet 模式")
		return fmt.Errorf(
			"refusing to modify unverified usbnet mode %q",
			currentMode,
		)
	}

	a.recordECMBootstrapAttempt()
	a.setECMBootstrapState(moduleInitInitializing, "正在启用 ECM", "")

	setResponse, err := a.runATCommand(`AT+QCFG="usbnet",1`, 8*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "写入 ECM 模式失败："+err.Error())
		return fmt.Errorf("write usbnet=1: %w", err)
	}
	if !atCommandSucceeded(setResponse) {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "模块未确认 ECM 模式写入成功")
		return errors.New("module did not confirm usbnet=1 write")
	}

	confirmResponse, err := a.runATCommand(`AT+QCFG="usbnet"`, 4*time.Second)
	if err != nil {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "复核 usbnet 失败："+err.Error())
		return fmt.Errorf("verify usbnet=1: %w", err)
	}

	if parseUSBNetMode(confirmResponse) != "1" {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "ECM 模式复核失败，模块未返回 usbnet=1")
		return errors.New("module did not report usbnet=1 after write")
	}

	a.setECMBootstrapState(moduleInitRebooting, "正在等待模块重启", "")
	rebootResponse, rebootErr := a.runATCommand("AT+CFUN=1,1", 3*time.Second)

	if rebootErr != nil {
		// Immediate USB loss after CFUN is normal for this firmware.
	} else if !atCommandSucceeded(rebootResponse) {
		a.setECMBootstrapState(moduleInitFailed, "初始化失败", "模块未确认重启命令")
		return errors.New("module did not confirm reboot command")
	}

	// Close the old libusb handle. The normal discovery flow will open the
	// newly enumerated interface and verify usbnet=1 on the next cycle.
	a.markUSBATDetached("automatic ECM initialization requested module reboot")

	return nil
}

type ecmBootstrapState struct {
	Status      string
	Message     string
	Error       string
	LastAttempt time.Time
	InProgress  bool
}

func (a *app) beginECMBootstrap(status, message string, auto bool) bool {
	a.ecmBootstrapMu.Lock()
	defer a.ecmBootstrapMu.Unlock()
	if a.ecmBootstrapInProgress {
		return false
	}
	if auto &&
		(a.ecmBootstrapLastResult == moduleInitFailed || a.ecmBootstrapLastResult == moduleInitRebooting) &&
		!a.ecmBootstrapLastAttempt.IsZero() &&
		time.Since(a.ecmBootstrapLastAttempt) < ecmFailedRetryWindow {
		return false
	}
	a.ecmBootstrapInProgress = true
	if auto && a.ecmBootstrapLastResult == moduleInitReady && status == moduleInitChecking {
		return true
	}
	a.ecmBootstrapLastResult = status
	a.ecmBootstrapMessage = message
	a.ecmBootstrapError = ""
	return true
}

func (a *app) endECMBootstrap() {
	a.ecmBootstrapMu.Lock()
	a.ecmBootstrapInProgress = false
	a.ecmBootstrapMu.Unlock()
}

func (a *app) recordECMBootstrapAttempt() {
	a.ecmBootstrapMu.Lock()
	a.ecmBootstrapLastAttempt = time.Now()
	a.ecmBootstrapMu.Unlock()
}

func (a *app) setECMBootstrapState(status, message, errText string) {
	a.ecmBootstrapMu.Lock()
	previous := a.ecmBootstrapLastResult
	if previous == status {
		a.ecmBootstrapMessage = message
		a.ecmBootstrapError = errText
		if status == moduleInitFailed {
			a.ecmBootstrapLastAttempt = time.Now()
		}
		a.ecmBootstrapMu.Unlock()
		return
	}
	a.ecmBootstrapLastResult = status
	a.ecmBootstrapMessage = message
	a.ecmBootstrapError = errText
	if status == moduleInitFailed {
		a.ecmBootstrapLastAttempt = time.Now()
	}
	a.ecmBootstrapMu.Unlock()
	logECMBootstrapStateChange(previous, status, message, errText)
}

func logECMBootstrapStateChange(previous, current, message, errText string) {
	if current == "" {
		return
	}
	if previous == "" {
		previous = "none"
	}
	detail := firstNonEmpty(errText, message)
	if detail != "" {
		log.Printf("automatic ECM bootstrap: status %s -> %s: %s", previous, current, detail)
		return
	}
	log.Printf("automatic ECM bootstrap: status %s -> %s", previous, current)
}

func (a *app) currentECMBootstrapState() ecmBootstrapState {
	a.ecmBootstrapMu.Lock()
	defer a.ecmBootstrapMu.Unlock()
	return ecmBootstrapState{
		Status:      a.ecmBootstrapLastResult,
		Message:     a.ecmBootstrapMessage,
		Error:       a.ecmBootstrapError,
		LastAttempt: a.ecmBootstrapLastAttempt,
		InProgress:  a.ecmBootstrapInProgress,
	}
}

type qdc507ECMIdentity struct {
	Manufacturer string
	Model        string
	Firmware     string
	USBCfg       string
	USBNetMode   string
	Supported    bool
}

func applyECMIdentityToStatus(status *ecmInitializationStatus, identity qdc507ECMIdentity, atTransport string) {
	status.Manufacturer = identity.Manufacturer
	status.Model = identity.Model
	status.Firmware = identity.Firmware
	status.USBNetMode = identity.USBNetMode
	status.USBCfg = identity.USBCfg
	status.ATTransport = atTransport
	status.Supported = identity.Supported
	status.NeedsInitialization = identity.Supported && identity.USBNetMode == "0"

	decision := decideECMInitialization(status.Supported, status.USBNetMode)
	status.Reason = decision.Reason
	status.InitializationStatus = decision.Status
	status.InitializationMessage = decision.Message
	status.InitializationError = decision.Error
	status.InitializationBusy = isECMInitializationBusy(decision.Status)
}

func isECMInitializationBusy(status string) bool {
	return status == moduleInitChecking ||
		status == moduleInitInitializing ||
		status == moduleInitRebooting
}

type ecmInitializationDecision struct {
	ShouldWrite bool
	Status      string
	Message     string
	Error       string
	Reason      string
}

func decideECMInitialization(supported bool, usbnetMode string) ecmInitializationDecision {
	if !supported {
		reason := "模块型号、固件身份或 USB 配置不在当前支持范围内"
		return ecmInitializationDecision{
			Status:  moduleInitDetected,
			Message: "发现新的 DJI 第一代 4G 模块",
			Error:   reason,
			Reason:  reason,
		}
	}
	switch usbnetMode {
	case "1":
		return ecmInitializationDecision{
			Status:  moduleInitReady,
			Message: "模块已准备完成",
			Reason:  "模块已经处于 ECM 模式",
		}
	case "0":
		return ecmInitializationDecision{
			ShouldWrite: true,
			Status:      moduleInitDetected,
			Message:     "发现新的 DJI 第一代 4G 模块",
			Reason:      "检测到原厂模式，可执行 ECM 初始化",
		}
	case "":
		reason := "无法解析模块的 usbnet 模式"
		return ecmInitializationDecision{
			Status:  moduleInitFailed,
			Message: "初始化失败",
			Error:   reason,
			Reason:  reason,
		}
	default:
		reason := "检测到未验证的 usbnet 模式"
		return ecmInitializationDecision{
			Status:  moduleInitFailed,
			Message: "初始化失败",
			Error:   reason,
			Reason:  reason,
		}
	}
}

func classifyQDC507ECMStatus(atiResponse, usbcfgResponse, usbnetResponse string) qdc507ECMIdentity {
	identityUpper := strings.ToUpper(atiResponse)
	usbcfg := parseUSBCfgValue(usbcfgResponse)
	out := qdc507ECMIdentity{
		Firmware:   parseUSBATFirmware(atiResponse),
		USBCfg:     usbcfg,
		USBNetMode: parseUSBNetMode(usbnetResponse),
	}
	if strings.Contains(identityUpper, "BAIWANG") {
		out.Manufacturer = "Baiwang"
	}
	if strings.Contains(identityUpper, "QDC507") {
		out.Model = "QDC507"
	}
	out.Supported = out.Manufacturer == "Baiwang" &&
		out.Model == "QDC507" &&
		normalizeUSBCfgValue(usbcfg) == supportedQDC507USBCfg
	return out
}

func parseUSBCfgValue(response string) string {
	replacer := strings.NewReplacer("\r", "\n")
	for _, line := range strings.Split(replacer.Replace(response), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "+qcfg:") || !strings.Contains(lower, `"usbcfg"`) {
			continue
		}
		index := strings.Index(lower, `"usbcfg"`)
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(line[index+len(`"usbcfg"`):])
		value = strings.TrimPrefix(value, ",")
		return strings.TrimSpace(value)
	}
	return ""
}

func normalizeUSBCfgValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
