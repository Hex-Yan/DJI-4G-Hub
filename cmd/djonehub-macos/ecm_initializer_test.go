package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestClassifyQDC507ECMStatus(t *testing.T) {
	ati := "Manufacturer: Baiwang\r\nModel: QDC507\r\nRevision: QDC507GLEFM21\r\nOK"
	usbcfg := `+QCFG: "usbcfg",0x2CA3,0x4006,1,1,1,1,1,0,0` + "\r\nOK"
	usbnet := `+QCFG: "usbnet",0` + "\r\nOK"

	status := classifyQDC507ECMStatus(ati, usbcfg, usbnet)
	if !status.Supported {
		t.Fatalf("expected supported Baiwang QDC507, got %+v", status)
	}
	if status.Manufacturer != "Baiwang" || status.Model != "QDC507" {
		t.Fatalf("identity not parsed: %+v", status)
	}
	if status.USBNetMode != "0" {
		t.Fatalf("USBNetMode = %q, want 0", status.USBNetMode)
	}

	wrongUSBCfg := `+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,0` + "\r\nOK"
	if classifyQDC507ECMStatus(ati, wrongUSBCfg, usbnet).Supported {
		t.Fatal("non-factory usbcfg must not be treated as auto ECM supported")
	}

	wrongModel := "Manufacturer: Baiwang\r\nModel: EG25G\r\nOK"
	if classifyQDC507ECMStatus(wrongModel, usbcfg, usbnet).Supported {
		t.Fatal("non-QDC507 module must not be treated as auto ECM supported")
	}
}

func TestParseUSBCfgValue(t *testing.T) {
	got := parseUSBCfgValue("AT+QCFG=\"usbcfg\"\r\n+QCFG: \"usbcfg\", 0x2CA3, 0x4006, 1, 1, 1, 1, 1, 0, 0\r\nOK")
	want := "0x2CA3, 0x4006, 1, 1, 1, 1, 1, 0, 0"
	if got != want {
		t.Fatalf("parseUSBCfgValue = %q, want %q", got, want)
	}
	if normalizeUSBCfgValue(got) != supportedQDC507USBCfg {
		t.Fatalf("normalized usbcfg = %q, want %q", normalizeUSBCfgValue(got), supportedQDC507USBCfg)
	}
}

func TestDecideECMInitializationTransitions(t *testing.T) {
	tests := []struct {
		name        string
		supported   bool
		mode        string
		wantStatus  string
		wantWrite   bool
		wantHasErro bool
	}{
		{name: "unsupported", supported: false, mode: "0", wantStatus: moduleInitDetected, wantWrite: false, wantHasErro: true},
		{name: "factory needs ECM", supported: true, mode: "0", wantStatus: moduleInitDetected, wantWrite: true},
		{name: "already ECM", supported: true, mode: "1", wantStatus: moduleInitReady, wantWrite: false},
		{name: "usbnet 2 blocked", supported: true, mode: "2", wantStatus: moduleInitFailed, wantWrite: false, wantHasErro: true},
		{name: "usbnet 3 blocked", supported: true, mode: "3", wantStatus: moduleInitFailed, wantWrite: false, wantHasErro: true},
		{name: "unknown blocked", supported: true, mode: "", wantStatus: moduleInitFailed, wantWrite: false, wantHasErro: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideECMInitialization(tt.supported, tt.mode)
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.ShouldWrite != tt.wantWrite {
				t.Fatalf("ShouldWrite = %v, want %v", got.ShouldWrite, tt.wantWrite)
			}
			if (got.Error != "") != tt.wantHasErro {
				t.Fatalf("Error = %q, want error presence %v", got.Error, tt.wantHasErro)
			}
		})
	}
}

func TestUSBNetOneSafeSkip(t *testing.T) {
	decision := decideECMInitialization(true, "1")
	if decision.ShouldWrite {
		t.Fatal("usbnet=1 must be treated as ready and never written again")
	}
	if decision.Status != moduleInitReady {
		t.Fatalf("status = %q, want %q", decision.Status, moduleInitReady)
	}
	if decision.Error != "" {
		t.Fatalf("unexpected error for usbnet=1: %s", decision.Error)
	}
}

func TestECMBootstrapStateTransitions(t *testing.T) {
	instance := &app{}
	if !instance.beginECMBootstrap(moduleInitChecking, "正在检查模块 ECM 状态", false) {
		t.Fatal("first transition into checking should be accepted")
	}
	if instance.beginECMBootstrap(moduleInitInitializing, "正在启用 ECM", false) {
		t.Fatal("concurrent initialization transition should be rejected")
	}
	instance.recordECMBootstrapAttempt()
	instance.setECMBootstrapState(moduleInitInitializing, "正在启用 ECM", "")
	state := instance.currentECMBootstrapState()
	if state.Status != moduleInitInitializing || !state.InProgress || state.LastAttempt.IsZero() {
		t.Fatalf("initializing state = %+v", state)
	}
	instance.setECMBootstrapState(moduleInitRebooting, "正在等待模块重启", "")
	instance.endECMBootstrap()
	state = instance.currentECMBootstrapState()
	if state.Status != moduleInitRebooting || state.InProgress {
		t.Fatalf("rebooting state = %+v", state)
	}
	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
	state = instance.currentECMBootstrapState()
	if state.Status != moduleInitReady || state.Error != "" {
		t.Fatalf("ready state = %+v", state)
	}
}

func TestECMBootstrapStateLogsOnlyOnStatusChange(t *testing.T) {
	var logs bytes.Buffer
	restore := captureECMLogs(&logs)
	defer restore()

	instance := &app{}
	instance.setECMBootstrapState(moduleInitDisconnected, "未检测到 DJI USB 设备", "")
	instance.setECMBootstrapState(moduleInitDetected, "发现新的 DJI 第一代 4G 模块", "")
	instance.setECMBootstrapState(moduleInitChecking, "正在检查模块 ECM 状态", "")
	instance.setECMBootstrapState(moduleInitInitializing, "正在启用 ECM", "")
	instance.setECMBootstrapState(moduleInitRebooting, "正在等待模块重启", "")
	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
	instance.setECMBootstrapState(moduleInitDisconnected, "未检测到 DJI USB 设备", "")
	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")

	output := logs.String()
	if strings.Contains(output, "ready -> ready") {
		t.Fatalf("ready -> ready should never be logged:\n%s", output)
	}
	if count := strings.Count(output, "-> "+moduleInitReady); count != 2 {
		t.Fatalf("ready transition log count = %d, want 2 after reconnect:\n%s", count, output)
	}
	if count := strings.Count(output, "-> "+moduleInitDisconnected); count != 2 {
		t.Fatalf("disconnected transition log count = %d, want 2:\n%s", count, output)
	}
}

func TestAutoReadyPollDoesNotRecheckTransitionOrLog(t *testing.T) {
	instance := &app{
		ecmBootstrapLastResult: moduleInitReady,
		ecmBootstrapMessage:    "模块已准备完成",
	}

	var logs bytes.Buffer
	restore := captureECMLogs(&logs)
	defer restore()

	if !instance.beginECMBootstrap(moduleInitChecking, "正在检查模块 ECM 状态", true) {
		t.Fatal("auto ready poll should be allowed to run")
	}
	state := instance.currentECMBootstrapState()
	if state.Status != moduleInitReady {
		t.Fatalf("auto ready poll changed status to %q, want ready", state.Status)
	}
	if !state.InProgress {
		t.Fatal("auto ready poll should still mark work in progress")
	}

	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
	instance.endECMBootstrap()

	if logs.String() != "" {
		t.Fatalf("repeated ready poll should not log or transition, got:\n%s", logs.String())
	}
	state = instance.currentECMBootstrapState()
	if state.Status != moduleInitReady || state.InProgress {
		t.Fatalf("final ready poll state = %+v", state)
	}
}

func TestReadyStatusSnapshotIsInternallyConsistent(t *testing.T) {
	status := ecmInitializationStatus{
		Connected:             true,
		InitializationStatus:  moduleInitReady,
		InitializationMessage: "模块已准备完成",
		InitializationBusy:    true,
	}
	identity := qdc507ECMIdentity{
		Manufacturer: "Baiwang",
		Model:        "QDC507",
		Firmware:     "QDC507GLEFM21",
		USBCfg:       "0x2CA3,0x4006,1,1,1,1,1,0,0",
		USBNetMode:   "1",
		Supported:    true,
	}

	applyECMIdentityToStatus(&status, identity, "USB AT · 2ca3:4006 interface 3")

	if !status.Connected {
		t.Fatal("ready snapshot should remain connected")
	}
	if !status.Supported {
		t.Fatalf("ready snapshot supported = false: %+v", status)
	}
	if status.NeedsInitialization {
		t.Fatalf("ready snapshot needs initialization: %+v", status)
	}
	if status.InitializationStatus != moduleInitReady {
		t.Fatalf("initialization status = %q, want ready", status.InitializationStatus)
	}
	if status.InitializationBusy {
		t.Fatalf("ready snapshot must not be busy: %+v", status)
	}
	if status.USBNetMode != "1" {
		t.Fatalf("usbnet mode = %q, want 1", status.USBNetMode)
	}
	if status.Manufacturer != "Baiwang" || status.Model != "QDC507" ||
		status.Firmware != "QDC507GLEFM21" || status.USBCfg == "" || status.ATTransport == "" {
		t.Fatalf("identity fields not populated: %+v", status)
	}
}

func TestBootstrapEndClearsBusy(t *testing.T) {
	instance := &app{}
	if !instance.beginECMBootstrap(moduleInitChecking, "正在检查模块 ECM 状态", true) {
		t.Fatal("begin bootstrap should succeed")
	}
	if state := instance.currentECMBootstrapState(); !state.InProgress {
		t.Fatalf("state should be busy after begin: %+v", state)
	}
	instance.setECMBootstrapState(moduleInitReady, "模块已准备完成", "")
	instance.endECMBootstrap()
	state := instance.currentECMBootstrapState()
	if state.InProgress {
		t.Fatalf("defer/end should clear bootstrap busy: %+v", state)
	}
	if state.Status != moduleInitReady {
		t.Fatalf("status = %q, want ready", state.Status)
	}
}

func TestConcurrentReadyPollStatusSnapshotHasNoContradiction(t *testing.T) {
	instance := &app{
		ecmBootstrapLastResult: moduleInitReady,
		ecmBootstrapMessage:    "模块已准备完成",
	}
	if !instance.beginECMBootstrap(moduleInitChecking, "正在检查模块 ECM 状态", true) {
		t.Fatal("auto ready poll should be allowed")
	}
	snapshot := instance.currentECMBootstrapState()
	if snapshot.Status != moduleInitReady || !snapshot.InProgress {
		t.Fatalf("test setup state = %+v", snapshot)
	}

	status := ecmInitializationStatus{
		Connected:             true,
		InitializationStatus:  snapshot.Status,
		InitializationMessage: snapshot.Message,
		InitializationBusy:    snapshot.InProgress,
	}
	identity := classifyQDC507ECMStatus(
		"Manufacturer: Baiwang\r\nModel: QDC507\r\nRevision: QDC507GLEFM21\r\nOK",
		`+QCFG: "usbcfg",0x2CA3,0x4006,1,1,1,1,1,0,0`+"\r\nOK",
		`+QCFG: "usbnet",1`+"\r\nOK",
	)

	applyECMIdentityToStatus(&status, identity, "USB AT · 2ca3:4006 interface 3")

	if status.InitializationStatus == moduleInitReady &&
		(!status.Supported || status.InitializationBusy || status.USBNetMode != "1") {
		t.Fatalf("contradictory ready snapshot: %+v", status)
	}
	instance.endECMBootstrap()
}

func TestExpectedDisconnectedPollErrorsStayQuiet(t *testing.T) {
	var logs bytes.Buffer
	restore := captureECMLogs(&logs)
	defer restore()

	for range 3 {
		logExpectedAware("SMS poll failed", errDJIUSBNotConnected)
		logExpectedAware("USB AT retry failed", fmt.Errorf("wrapped: %w", errDJIUSBNotConnected))
	}

	if shouldLogPollError(errDJIUSBNotConnected) {
		t.Fatal("expected disconnected sentinel should not be logged")
	}
	if !isExpectedDisconnectedError(fmt.Errorf("wrapped: %w", errDJIUSBNotConnected)) {
		t.Fatal("wrapped disconnected sentinel should be recognized")
	}
	if logs.String() != "" {
		t.Fatalf("expected disconnected polls should stay quiet, got:\n%s", logs.String())
	}
}

func TestRepeatedDisconnectedEnsureUSBATLogsSingleStateChange(t *testing.T) {
	var logs bytes.Buffer
	restore := captureECMLogs(&logs)
	defer restore()

	instance := &app{
		usbDeviceScanner: func() *usbDeviceStatus {
			return nil
		},
	}

	for range 3 {
		if err := instance.ensureUSBAT(); !errors.Is(err, errDJIUSBNotConnected) {
			t.Fatalf("ensureUSBAT error = %v, want disconnected sentinel", err)
		}
	}

	output := logs.String()
	if count := strings.Count(output, "-> "+moduleInitDisconnected); count != 1 {
		t.Fatalf("disconnected state change log count = %d, want 1:\n%s", count, output)
	}
	if strings.Contains(output, "SMS poll failed") || strings.Contains(output, "USB AT retry failed") {
		t.Fatalf("disconnected polling should not log poll failures:\n%s", output)
	}
}

func TestUnexpectedPollErrorsAreLogged(t *testing.T) {
	var logs bytes.Buffer
	restore := captureECMLogs(&logs)
	defer restore()

	err := errors.New("USB bulk read: LIBUSB_ERROR_IO")
	logExpectedAware("SMS poll failed", err)
	logExpectedAware("USB AT retry failed", err)

	output := logs.String()
	if !strings.Contains(output, "SMS poll failed: USB bulk read: LIBUSB_ERROR_IO") {
		t.Fatalf("unexpected SMS error was not logged:\n%s", output)
	}
	if !strings.Contains(output, "USB AT retry failed: USB bulk read: LIBUSB_ERROR_IO") {
		t.Fatalf("unexpected USB AT error was not logged:\n%s", output)
	}
}

func TestDisconnectedPollingKeepsScanningForReconnect(t *testing.T) {
	scans := 0
	instance := &app{
		usbDeviceScanner: func() *usbDeviceStatus {
			scans++
			if scans < 3 {
				return nil
			}
			return &usbDeviceStatus{
				Product:   "Baiwang",
				Vendor:    "DJI",
				VendorID:  "2ca3",
				ProductID: "4006",
				Mode:      "vendor-specific USB mode",
			}
		},
	}

	if err := instance.ensureUSBAT(); !errors.Is(err, errDJIUSBNotConnected) {
		t.Fatalf("first disconnected poll error = %v, want sentinel", err)
	}
	if err := instance.ensureUSBAT(); !errors.Is(err, errDJIUSBNotConnected) {
		t.Fatalf("second disconnected poll error = %v, want sentinel", err)
	}
	if scans != 2 {
		t.Fatalf("disconnected polling scans = %d, want 2", scans)
	}

	err := instance.ensureUSBAT()
	if err == nil {
		t.Fatal("test environment should still fail opening USB AT without a real handle")
	}
	if scans != 3 {
		t.Fatalf("reconnect poll scans = %d, want 3", scans)
	}
	if state := instance.currentECMBootstrapState(); state.Status != moduleInitDetected {
		t.Fatalf("reconnect should advance to detected before opening AT, got %+v", state)
	}
	if !shouldLogPollError(err) {
		t.Fatalf("open failure after detected device is unexpected and should be loggable: %v", err)
	}
}

func captureECMLogs(buffer *bytes.Buffer) func() {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	return func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	}
}
