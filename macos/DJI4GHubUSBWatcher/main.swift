import AppKit
import Foundation
import Darwin
import IOKit
import IOKit.usb

private let djiVendorID = 0x2CA3
private let djiProductID = 0x4006
private let preferredAppURL =
    URL(fileURLWithPath: "/Applications/DJI 4G Hub.app")

private let appBundleIdentifier =
    "com.test.DJI-4G-Hub-App"

final class USBWatcher {
    private var notificationPort: IONotificationPortRef?
    private var addedIterator: io_iterator_t = 0

    func start() {
        scanExistingDevices()

        guard let matching = IOServiceMatching(kIOUSBDeviceClassName) as NSMutableDictionary? else {
            fputs("无法创建 USB 匹配条件。\n", stderr)
            exit(1)
        }

        matching[kUSBVendorID] = djiVendorID
        matching[kUSBProductID] = djiProductID

        guard let port = IONotificationPortCreate(kIOMainPortDefault) else {
            fputs("无法创建 IOKit 通知端口。\n", stderr)
            exit(1)
        }

        notificationPort = port

        guard let runLoopSource = IONotificationPortGetRunLoopSource(port)?.takeUnretainedValue() else {
            fputs("无法取得 IOKit RunLoop Source。\n", stderr)
            exit(1)
        }

        CFRunLoopAddSource(
            CFRunLoopGetCurrent(),
            runLoopSource,
            .defaultMode
        )

        let context = Unmanaged.passUnretained(self).toOpaque()

        let result = IOServiceAddMatchingNotification(
            port,
            kIOFirstMatchNotification,
            matching,
            { context, iterator in
                guard let context else { return }

                let watcher = Unmanaged<USBWatcher>
                    .fromOpaque(context)
                    .takeUnretainedValue()

                watcher.handleDevices(iterator)
            },
            context,
            &addedIterator
        )

        guard result == KERN_SUCCESS else {
            fputs("无法注册 USB 插入通知：\(result)\n", stderr)
            exit(1)
        }

        // 必须读取一次 iterator，才能激活后续热插拔通知。
        handleDevices(addedIterator)

        print("DJI 4G Hub USB Watcher 已启动。")
        print(
            String(
                format: "正在监听 VID 0x%04X / PID 0x%04X",
                djiVendorID,
                djiProductID
            )
        )

        CFRunLoopRun()
    }

    private func scanExistingDevices() {
        guard let matching = IOServiceMatching(kIOUSBDeviceClassName) as NSMutableDictionary? else {
            return
        }

        matching[kUSBVendorID] = djiVendorID
        matching[kUSBProductID] = djiProductID

        var iterator: io_iterator_t = 0
        let result = IOServiceGetMatchingServices(
            kIOMainPortDefault,
            matching,
            &iterator
        )

        guard result == KERN_SUCCESS else {
            fputs("启动扫描 USB 设备失败：\(result)\n", stderr)
            return
        }

        defer {
            IOObjectRelease(iterator)
        }

        let service = IOIteratorNext(iterator)

        if service != 0 {
            IOObjectRelease(service)
            print("启动时已检测到 DJI 4G 模块。")
            launchAppIfNeeded()
        } else {
            print("启动时未检测到 DJI 4G 模块，继续监听热插拔。")
        }
    }

    private func handleDevices(_ iterator: io_iterator_t) {
        var service = IOIteratorNext(iterator)

        while service != 0 {
            IOObjectRelease(service)

            DispatchQueue.main.async { [weak self] in
                self?.launchAppIfNeeded()
            }

            service = IOIteratorNext(iterator)
        }
    }

    private func launchAppIfNeeded() {
        let appURL: URL?

        if FileManager.default.fileExists(atPath: preferredAppURL.path) {
            appURL = preferredAppURL
        } else {
            appURL = NSWorkspace.shared.urlForApplication(
                withBundleIdentifier: appBundleIdentifier
            )
        }

        guard let appURL else {
            fputs(
                "未找到 DJI 4G Hub（Bundle ID: \(appBundleIdentifier)）\n",
                stderr
            )
            return
        }

        print("准备启动：\(appURL.path)")

        let appAlreadyRunning = NSWorkspace.shared.runningApplications.contains {
            $0.bundleIdentifier == appBundleIdentifier
        }

        guard !appAlreadyRunning else {
            print("DJI 4G Hub 已经运行，无需重复启动。")
            return
        }

        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        configuration.addsToRecentItems = false

        NSWorkspace.shared.openApplication(
            at: appURL,
            configuration: configuration
        ) { _, error in
            if let error {
                fputs("启动 DJI 4G Hub 失败：\(error.localizedDescription)\n", stderr)
            } else {
                print("检测到 DJI 4G 模块，已启动 DJI 4G Hub。")
            }
        }
    }
}

setbuf(stdout, nil)
setbuf(stderr, nil)

USBWatcher().start()
