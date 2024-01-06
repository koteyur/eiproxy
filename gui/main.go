//go:build windows

package main

import (
	"context"
	"eiproxy/client"
	"eiproxy/protocol"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	dec "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	version            = "0.1.0"
	mwWidth            = 280
	mwHeight           = 300
	mwTitle            = "EI Proxy"
	userKeyPlaceholder = "Put your access key here"
	webSite            = "https://ei.koteyur.dev/proxy"
)

type config struct {
	MasterAddr string
	ServerURL  string
	UserKey    string
}

var (
	mainWnd         *walk.MainWindow
	startBt, stopBt *walk.PushButton

	cfg           config
	defaultConfig = config{
		ServerURL:  "https://ei.koteyur.dev/proxy",
		MasterAddr: "vps.gipat.ru:28004",
		UserKey:    userKeyPlaceholder,
	}

	stopAndWait = func() {}
)

func main() {
	defer ensureSingleAppInstance()()

	err := dec.MainWindow{
		Font:     dec.Font{PointSize: walk.IntFrom96DPI(12, 96)},
		Title:    mwTitle,
		AssignTo: &mainWnd,
		Size:     dec.Size{Width: mwWidth, Height: mwHeight},
		OnSizeChanged: func() {
			_ = mainWnd.SetSize(walk.Size{Width: mwWidth, Height: mwHeight})
		},

		Layout: dec.Grid{Columns: 1, MarginsZero: true, SpacingZero: true},
		Children: []dec.Widget{
			dec.Composite{
				Layout: dec.Grid{Columns: 1},
				Children: []dec.Widget{
					// dec.Composite{
					// 	Layout: dec.Grid{Columns: 3, MarginsZero: true},
					// 	Children: []dec.Widget{
					dec.PushButton{
						Text:      "Start",
						OnClicked: start,
						AssignTo:  &startBt,
					},
					// dec.CheckBox{
					// 	Text: "Autorun",
					// },
					dec.PushButton{
						Text:      "Stop",
						Enabled:   false,
						OnClicked: func() {},
						AssignTo:  &stopBt,
					},
					// 	},
					// },
					// dec.PushButton{
					// 	Text:      "Test connection",
					// 	OnClicked: testConnection,
					// },
					// dec.PushButton{
					// 	Text:      "Edit Config",
					// 	OnClicked: runConfigDialog,
					// },
					dec.PushButton{
						Text: "Exit",
						OnClicked: func() {
							mainWnd.Close()
							walk.App().Exit(0)
						},
					},
				},
			},

			// Status bar. Not using normal one, as it has sizing grip, which I couldn't disable
			dec.VSpacer{},
			dec.VSeparator{},
			dec.Composite{
				Layout:    dec.HBox{Margins: dec.Margins{Left: 5, Right: 5, Bottom: 2, Top: 2}},
				Alignment: dec.AlignHFarVCenter,
				MaxSize:   dec.Size{Height: 20},
				Children: []dec.Widget{
					dec.HSpacer{},
					dec.HSeparator{},
					dec.LinkLabel{
						Text: fmt.Sprintf(`<a id="this" href="%s">website</a>`, webSite),
						OnLinkActivated: func(link *walk.LinkLabelLink) {
							_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", link.URL()).Start()
						},
					},
					dec.HSeparator{},
					dec.Label{Text: fmt.Sprintf("ver. %s", version)},
				},
			},
		},
	}.Create()
	if err != nil {
		log.Fatal(err)
	}

	// Disable maximize button and set window not resizable
	win.SetWindowLong(mainWnd.Handle(), win.GWL_STYLE,
		win.GetWindowLong(mainWnd.Handle(), win.GWL_STYLE) & ^win.WS_MAXIMIZEBOX & ^win.WS_SIZEBOX)

	// Try to set main window icon.
	// ID of GrpIcon assigned by rsrc tool: rsrc -manifest app.manifest -ico app.ico -o rsrc.syso
	const appIconID = 2
	var appIcon *walk.Icon
	if icon, err := walk.NewIconFromResourceId(appIconID); err == nil {
		appIcon = icon
	}

	_ = mainWnd.SetIcon(appIcon)

	ni := createTrayIcon(mainWnd, appIcon)
	defer func() { _ = ni.Dispose() }()

	mainWnd.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		stopAndWait()
	})

	mainWnd.Run()
}

func start() {
	loadConfig()

	if cfg.UserKey == userKeyPlaceholder || cfg.UserKey == "" {
		showErrorF(
			"Please enter your access key in eiproxy.json. "+
				"You can get it at %s (click website in the bottom right corner).",
			webSite)
		return
	}
	userKey, err := protocol.UserKeyFromString(strings.ToUpper(strings.TrimSpace(cfg.UserKey)))
	if err != nil {
		showErrorF("Invalid access key: %v", err)
		return
	}

	clientCfg := client.Config{
		MasterAddr: cfg.MasterAddr,
		ServerURL:  cfg.ServerURL,
		UserKey:    userKey,
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := client.New(clientCfg)

	// Disable start button and enable stop button.
	startBt.SetEnabled(false)
	stopBt.SetEnabled(true)
	handle := stopBt.Clicked().Attach(func() { cancel() })

	// Override master addr in:
	// - HKCU\Software\Gipat.Ru\EI_Starter\EvilIslands\Network Settings\Master Server Name
	// - Software\Nival Interactive\EvilIslands\Network Settings\Master Server Name
	const (
		HKCU           = win.HKEY_CURRENT_USER
		gameKeyPath    = `Software\Nival Interactive\EvilIslands\Network Settings`
		starterKeyPath = `Software\Gipat.Ru\EI_Starter\EvilIslands\Network Settings`
	)
	prevGame, err := registryKeyString(HKCU, gameKeyPath, "Master Server Name")
	if err == nil {
		err = setRegistryKeyString(HKCU, gameKeyPath, "Master Server Name", "127.0.0.1:28004")
		if err != nil {
			showErrorF("Failed to override game's master addr: %v", err)
			return
		}
	}

	prevStarter, err := registryKeyString(HKCU, starterKeyPath, "Master Server Name")
	if err == nil {
		err = setRegistryKeyString(HKCU, starterKeyPath, "Master Server Name", "127.0.0.1:28004")
		if err != nil {
			showErrorF("Failed to override starter's master addr: %v", err)
			return
		}
	}

	done := make(chan struct{})
	noUpdateUI := false
	stopAndWait = func() { noUpdateUI = true; cancel(); <-done }
	go func() {
		defer close(done)
		defer cancel()
		err := c.Run(ctx)
		if err != nil {
			showErrorF("Client error: %v", err)
		}

		// Restore master addr in registry.
		if prevGame != "" {
			err = setRegistryKeyString(HKCU, gameKeyPath, "Master Server Name", prevGame)
			if err != nil {
				showErrorF("Failed to restore game's master addr: %v", err)
			}
		}
		if prevStarter != "" {
			err = setRegistryKeyString(HKCU, starterKeyPath, "Master Server Name", prevStarter)
			if err != nil {
				showErrorF("Failed to restore starter's master addr: %v", err)
			}
		}

		if !noUpdateUI {
			stopBt.SetEnabled(false)
			startBt.SetEnabled(true)
			stopBt.Clicked().Detach(handle)
		}
		stopAndWait = func() {}
	}()
}

func createTrayIcon(mw *walk.MainWindow, icon *walk.Icon) *walk.NotifyIcon {
	// Create the notify icon and make sure we clean it up on exit.
	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		fatal(err)
	}

	// Set the icon and a tool tip text.
	if err := ni.SetIcon(icon); err != nil {
		fatal(err)
	}
	if err := ni.SetToolTip(mwTitle); err != nil {
		fatal(err)
	}

	// Hide to tray on minimize.
	var prevWndProc uintptr
	prevWndProc = win.SetWindowLongPtr(mw.Handle(), win.GWLP_WNDPROC,
		syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
			switch msg {
			case win.WM_SYSCOMMAND:
				switch wParam {
				case win.SC_MINIMIZE:
					mw.Hide()
					return 0
				}
			}
			return win.CallWindowProc(prevWndProc, hwnd, msg, wParam, lParam)
		}),
	)

	// Hide to tray on close.
	// mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
	// 	if reason == walk.CloseReasonUnknown {
	// 		mw.Hide()
	// 		*canceled = true
	// 	}
	// })

	// Show main window on click.
	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			mw.Show()
			win.SetForegroundWindow(mw.Handle())
			win.ShowWindow(mw.Handle(), win.SW_RESTORE)
		}
	})

	// We put an exit action into the context menu.
	exitAction := walk.NewAction()
	if err := exitAction.SetText("E&xit"); err != nil {
		fatal(err)
	}
	exitAction.Triggered().Attach(func() { walk.App().Exit(0) })
	if err := ni.ContextMenu().Actions().Add(exitAction); err != nil {
		fatal(err)
	}

	// The notify icon is hidden initially, so we have to make it visible.
	if err := ni.SetVisible(true); err != nil {
		fatal(err)
	}

	return ni
}

func ensureSingleAppInstance() func() {
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr("EIProxyClient"))
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			const walkWindowClass = `\o/ Walk_MainWindow_Class \o/`
			hWnd := win.FindWindow(windows.StringToUTF16Ptr(walkWindowClass),
				windows.StringToUTF16Ptr(mwTitle))
			if hWnd != 0 {
				win.ShowWindow(hWnd, win.SW_RESTORE)
				win.SetForegroundWindow(hWnd)
			}
			os.Exit(0)
		}
		fatal(err)
	}
	return func() {
		_ = windows.CloseHandle(handle)
	}
}

func loadConfig() {
	// Load eiproxy.json and if it doesn't exist create new one using default.
	// If fail, show message box and exit.
	data, err := os.ReadFile("eiproxy.json")
	if err != nil {
		if os.IsNotExist(err) {
			data, err = json.MarshalIndent(defaultConfig, "", "  ")
			if err != nil {
				fatal(err)
			}
			err = os.WriteFile("eiproxy.json", data, 0644)
			if err != nil {
				fatal(err)
			}
			cfg = defaultConfig
			return
		}
		fatal(err)
	}

	// Try to unmarshal config file.
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		fatal(err)
	}
}

func registryKeyString(rootKey win.HKEY, subKeyPath, valueName string) (value string, err error) {
	subKeyPathUTF16, _ := syscall.UTF16PtrFromString(subKeyPath)
	valueNameUTF16, _ := syscall.UTF16PtrFromString(valueName)

	var hKey win.HKEY
	if win.RegOpenKeyEx(
		rootKey,
		subKeyPathUTF16,
		0,
		win.KEY_READ,
		&hKey) != win.ERROR_SUCCESS {

		return "", errors.New("registry key not found")
	}
	defer win.RegCloseKey(hKey)

	var typ uint32
	var data []uint16
	var bufSize uint32

	if win.ERROR_SUCCESS != win.RegQueryValueEx(
		hKey,
		valueNameUTF16,
		nil,
		&typ,
		nil,
		&bufSize) {

		return "", errors.New("registry value not found")
	}

	data = make([]uint16, bufSize/2+1)

	if win.ERROR_SUCCESS != win.RegQueryValueEx(
		hKey,
		valueNameUTF16,
		nil,
		&typ,
		(*byte)(unsafe.Pointer(&data[0])),
		&bufSize) {

		return "", errors.New("registry value not found")
	}

	return syscall.UTF16ToString(data), nil
}

func setRegistryKeyString(rootKey win.HKEY, subKeyPath, valueName, value string) error {
	subKeyPathUTF16, _ := syscall.UTF16PtrFromString(subKeyPath)
	valueNameUTF16, _ := syscall.UTF16PtrFromString(valueName)
	valueUTF16, _ := syscall.UTF16PtrFromString(value)

	tmp, _ := syscall.UTF16FromString(value)
	valueUTF16Len := len(tmp)

	var hKey win.HKEY
	if win.RegOpenKeyEx(
		rootKey,
		subKeyPathUTF16,
		0,
		win.KEY_WRITE,
		&hKey) != win.ERROR_SUCCESS {

		return errors.New("registry key not found")
	}
	defer win.RegCloseKey(hKey)

	if win.ERROR_SUCCESS != win.RegSetValueEx(
		hKey,
		valueNameUTF16,
		0,
		win.REG_SZ,
		(*byte)(unsafe.Pointer(valueUTF16)),
		uint32(valueUTF16Len*2)) {

		return errors.New("failed to set registry value")
	}

	return nil
}

func getAndShowMainWindow() walk.Form {
	if mainWnd != nil {
		if !mainWnd.Visible() {
			mainWnd.Show()
			win.SetForegroundWindow(mainWnd.Handle())
			win.ShowWindow(mainWnd.Handle(), win.SW_RESTORE)
		}
		return mainWnd
	}
	return nil
}

func showErrorF(format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	walk.MsgBox(getAndShowMainWindow(), "Error", text, walk.MsgBoxIconError)
}

func fatal(err error) {
	showErrorF("%v", err)
	os.Exit(1)
}
