//go:build windows

package main

import (
	"context"
	"eiproxy/client"
	"eiproxy/protocol"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/lxn/walk"
	dec "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	mwWidth            = 280
	mwHeight           = 300
	mwTitle            = "EI Proxy"
	userKeyPlaceholder = "Put your access key here"
	webSite            = "https://ei.koteyur.dev/proxy"
)

var (
	mainWnd         *walk.MainWindow
	startBt, stopBt *walk.PushButton
	proxyStatus     *walk.TextEdit
	proxyIPEdit     *walk.TextEdit

	stopAndWait = func() {}

	errKeyUnauthorized   = errors.New("key unauthorized")
	errServerMaintenance = errors.New("server maintenance")
	errServerInvalid     = errors.New("server invalid")
	errNetwork           = errors.New("network error")
)

func main() {
	defer ensureSingleAppInstance()()

	// Try to set main window icon.
	// ID of GrpIcon assigned by rsrc tool: rsrc -manifest app.manifest -ico app.ico -o rsrc.syso
	const appIconID = 2
	var appIcon *walk.Icon
	if icon, err := walk.NewIconFromResourceId(appIconID); err == nil {
		appIcon = icon
	}

	err := dec.MainWindow{
		Font:     dec.Font{PointSize: walk.IntFrom96DPI(12, 96)},
		Title:    mwTitle,
		AssignTo: &mainWnd,
		Size:     dec.Size{Width: mwWidth, Height: mwHeight},
		OnSizeChanged: func() {
			_ = mainWnd.SetSize(walk.Size{Width: mwWidth, Height: mwHeight})
		},
		Layout: dec.VBox{
			MarginsZero: true,
			SpacingZero: true,
		},
		Children: []dec.Widget{
			dec.Composite{
				Layout: dec.HBox{},
				Children: []dec.Widget{
					dec.PushButton{
						Text:      "Start",
						OnClicked: start,
						AssignTo:  &startBt,
					},
					dec.PushButton{
						Text:      "Stop",
						Enabled:   false,
						OnClicked: func() {},
						AssignTo:  &stopBt,
					},
				},
			},
			dec.Composite{
				Layout:    dec.Grid{Columns: 2},
				MaxSize:   dec.Size{Height: 10},
				Alignment: dec.AlignHCenterVNear,
				Children: []dec.Widget{
					dec.TextLabel{
						Text: "Status:",
					},
					dec.TextEdit{
						Font:          dec.Font{PointSize: walk.IntFrom96DPI(9, 96)},
						Text:          "stopped",
						Enabled:       false,
						ReadOnly:      true,
						TextAlignment: dec.AlignFar,
						AssignTo:      &proxyStatus,
					},
					dec.TextLabel{
						Text: "Proxy IP:",
					},
					dec.TextEdit{
						Font:          dec.Font{PointSize: walk.IntFrom96DPI(9, 96)},
						Text:          "unassigned",
						Enabled:       false,
						ReadOnly:      true,
						TextAlignment: dec.AlignFar,
						AssignTo:      &proxyIPEdit,
					},
				},
			},

			dec.VSpacer{},

			dec.Composite{
				Layout: dec.HBox{},
				Children: []dec.Widget{
					dec.HSpacer{},
					dec.PushButton{
						Text: "About",
						OnClicked: func() {
							showAbout(appIcon)
						},
					},
				},
			},

			// Status bar. Not using normal one, as it has sizing grip, which I couldn't disable.
			dec.VSeparator{},
			dec.Composite{
				Layout:    dec.HBox{Margins: dec.Margins{Left: 5, Right: 5, Bottom: 2, Top: 2}},
				Alignment: dec.AlignHFarVCenter,
				MaxSize:   dec.Size{Height: 20},
				Children: []dec.Widget{
					dec.HSpacer{},
					dec.HSeparator{},
					dec.LinkLabel{
						Text:            fmt.Sprintf(`<a id="this" href="%s">website</a>`, webSite),
						OnLinkActivated: onLinkActivated,
					},
					dec.HSeparator{},
					dec.Label{Text: fmt.Sprintf("ver. %s", client.ClientVer)},
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

	if cfg.UserKey == "" {
		ok := showEnterKeyDialog("")
		if !ok {
			return
		}
	} else {
		if err := checkKey(cfg.UserKey); err != nil {
			tryAgainMessage := ""
			if errors.Is(err, protocol.ErrInvalidKey) {
				tryAgainMessage = "Key has invalid format. Please try again."
			} else if errors.Is(err, errKeyUnauthorized) {
				tryAgainMessage = "It seems your access key is invalid. Please try again."
			} else if errors.Is(err, errServerMaintenance) {
				showErrorF("Server is under maintenance. Please try again later.\n\nError: %v", err)
				return
			} else if errors.Is(err, errServerInvalid) {
				showErrorF("Server returned invalid response. If you changed server address "+
					"in eiproxy.json, please check it.\n\nError: %v", err)
				return
			} else if errors.Is(err, errNetwork) {
				showErrorF("Failed to connect to server. Please check your internet connection."+
					"\n\nError: %v", err)
				return
			} else {
				showErrorF("Failed to check access key: %v", err)
				return
			}

			if ok := showEnterKeyDialog(tryAgainMessage); !ok {
				return
			}
		}
	}

	userKey, err := protocol.UserKeyFromString(cfg.UserKey)
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
	proxyStatus.SetText("starting...")
	handle := stopBt.Clicked().Attach(func() { cancel() })

	if isGameRunning() {
		showWarningF("Game is running. Please RESTART it. " +
			"Otherwise your server might be unavailable for other players.")
	}

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
		log.Printf("Client stopped: %v", err)
		if err != nil && !errors.Is(err, context.Canceled) {
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
			proxyIPEdit.SetEnabled(false)
			proxyIPEdit.SetText("")
			proxyStatus.SetText("stopped")
			stopBt.Clicked().Detach(handle)
		}
		stopAndWait = func() {}
	}()

	go func() {
		addr := c.GetProxyAddr(5000 * time.Millisecond)
		if addr == "" {
			return
		}
		proxyIPEdit.SetEnabled(true)
		proxyIPEdit.SetText(addr)
		proxyStatus.SetText("started")
		stopBt.SetEnabled(true)
	}()
}

func showEnterKeyDialog(reason string) bool {
	var dlg *walk.Dialog
	var keyEdit *walk.LineEdit
	var buttonOk, buttonCancel *walk.PushButton

	var key string

	text := ""
	if reason != "" {
		text += reason + "\n\n"
	}
	text += "Please enter your access key. You can get it here: " +
		fmt.Sprintf(`<a id="this" href="%s">%s</a>`, webSite, webSite)

	_ = dec.Dialog{
		AssignTo:      &dlg,
		Title:         "Enter access key",
		Icon:          walk.IconQuestion(),
		DefaultButton: &buttonOk,
		CancelButton:  &buttonCancel,
		MinSize:       dec.Size{Width: 400, Height: 150},
		Layout:        dec.VBox{},
		Font: dec.Font{
			PointSize: walk.IntFrom96DPI(10, 96),
		},
		Children: []dec.Widget{
			dec.LinkLabel{
				Text:            text,
				OnLinkActivated: onLinkActivated,
				MaxSize: dec.Size{
					Width:  300,
					Height: 100,
				},
			},
			dec.LineEdit{
				AssignTo:     &keyEdit,
				Text:         "",
				PasswordMode: true,
				OnTextChanged: func() {
					buttonOk.SetEnabled(keyEdit.Text() != "")
				},
			},
			dec.Composite{
				Layout: dec.HBox{},
				Children: []dec.Widget{
					dec.PushButton{
						AssignTo: &buttonOk,
						Text:     "OK",
						Enabled:  false,
						OnClicked: func() {
							key = keyEdit.Text()
							err := checkKey(key)
							if err != nil {
								if errors.Is(err, protocol.ErrInvalidKey) {
									showErrorF("Invalid access key format! Please make sure you entered it correctly.")
									return
								}
								showErrorF("Failed to check access key: %v", err)
								return
							}
							dlg.Accept()
						},
					},
					dec.PushButton{
						AssignTo: &buttonCancel,
						Text:     "Cancel",
						OnClicked: func() {
							dlg.Cancel()
						},
					},
				},
			},
		},
	}.Create(getAndShowMainWindow())

	if dlg.Run() != walk.DlgCmdOK {
		return false
	}

	cfg.UserKey = key
	saveConfig()
	return true
}

func showAbout(icon walk.Image) {
	var aboutText = `Tool for setting up public servers in the Evil Islands game without requiring a public IP or VPN. It's free and open source.

- Version: ` + client.ClientVer + `
- Author: Yury Kotov (aka Demoth)
- Site: <a href="` + webSite + `">` + webSite + `</a>
- Source code: <a href="https://github.com/koteyur/eiproxy">https://github.com/koteyur/eiproxy</a>

Third party components used:
- Walk: <a href="https://github.com/lxn/walk">https://github.com/lxn/walk</a>
- Win: <a href="https://github.com/lxn/win">https://github.com/lxn/win</a>
`

	var btnOk *walk.PushButton
	var dlg *walk.Dialog
	_ = dec.Dialog{
		AssignTo:      &dlg,
		Title:         "About",
		Icon:          icon,
		Font:          dec.Font{PointSize: walk.IntFrom96DPI(10, 96)},
		CancelButton:  &btnOk,
		DefaultButton: &btnOk,
		Layout:        dec.VBox{},
		Children: []dec.Widget{
			dec.ImageView{
				Image:   icon,
				MinSize: dec.Size{Width: 64, Height: 64},
				Mode:    dec.ImageViewModeZoom,
			},
			dec.TextLabel{
				Font:          dec.Font{PointSize: walk.IntFrom96DPI(12, 96), Bold: true},
				TextAlignment: dec.AlignHCenterVCenter,
				Text:          mwTitle,
			},
			dec.LinkLabel{
				OnLinkActivated: onLinkActivated,
				MaxSize:         dec.Size{Width: 300},
				Text:            aboutText,
			},
			dec.Composite{
				Layout: dec.HBox{},
				Children: []dec.Widget{
					dec.HSpacer{},
					dec.PushButton{
						AssignTo: &btnOk,
						Text:     "OK",
						OnClicked: func() {
							dlg.Accept()
						},
					},
					dec.HSpacer{},
				},
			},
		},
	}.Create(mainWnd)

	_ = dlg.Run()
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

func checkKey(key string) error {
	key = normalizeKey(key)

	_, err := protocol.UserKeyFromString(key)
	if err != nil {
		return err
	}

	url, _ := url.JoinPath(cfg.ServerURL, "api/user")
	err = apiRequest(http.MethodGet, url, key, nil, nil)
	if err != nil {
		var httpErr httpError
		if errors.As(err, &httpErr) {
			switch httpErr {
			case http.StatusUnauthorized:
				return fmt.Errorf("%w: %w", errKeyUnauthorized, err)
			case http.StatusServiceUnavailable:
				return fmt.Errorf("%w: %w", errServerMaintenance, err)
			default:
				return fmt.Errorf("%w: %w", errServerInvalid, err)
			}
		}
		return fmt.Errorf("%w: %w", errNetwork, err)
	}

	return nil
}

func isGameRunning() bool {
	hWnd := win.FindWindow(windows.StringToUTF16Ptr("EIGAME"),
		windows.StringToUTF16Ptr("Evil Islands"))
	return hWnd != 0
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

func fatal(err error) {
	showErrorF("%v", err)
	os.Exit(1)
}

func showWarningF(format string, args ...interface{}) {
	showMessageF("Warning", walk.MsgBoxIconWarning, format, args...)
}

func showErrorF(format string, args ...interface{}) {
	showMessageF("Error", walk.MsgBoxIconError, format, args...)
}

func showMessageF(title string, style walk.MsgBoxStyle, format string, args ...interface{}) {
	owner := walk.App().ActiveForm()
	if owner == nil {
		owner = getAndShowMainWindow()
	}
	msgBox(owner, title, style, format, args...)
}
