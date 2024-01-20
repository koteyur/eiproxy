//go:build windows

package main

import (
	"bytes"
	"context"
	"eiproxy/client"
	"eiproxy/protocol"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

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

type config struct {
	MasterAddr string
	ServerURL  string
	UserKey    string
}

var (
	mainWnd         *walk.MainWindow
	startBt, stopBt *walk.PushButton
	proxyStatus     *walk.TextEdit
	proxyIPEdit     *walk.TextEdit

	cfg           config
	defaultConfig = config{
		ServerURL:  webSite,
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

			// Status bar. Not using normal one, as it has sizing grip, which I couldn't disable.
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
		ok := showEnterKeyDialog("")
		if !ok {
			return
		}
	} else {
		if err := checkKey(cfg.UserKey); err != nil {
			ok := showEnterKeyDialog("Failed to check access key: " + err.Error())
			if !ok {
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
		text += reason + ".\n\n"
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

// getConfigPath returns path to config file in the same directory as executable.
func getConfigPath() string {
	exePath, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	dir := filepath.Dir(exePath)
	return filepath.Join(dir, "eiproxy.json")
}

func loadConfig() {
	configPath := getConfigPath()

	// Load eiproxy.json and if it doesn't exist create new one using default.
	// If fail, show message box and exit.
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = json.MarshalIndent(defaultConfig, "", "  ")
			if err != nil {
				fatal(err)
			}
			err = os.WriteFile(configPath, data, 0644)
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

	if cfg.UserKey == userKeyPlaceholder {
		cfg.UserKey = ""
	}

	cfg.UserKey = normalizeKey(cfg.UserKey)
}

func saveConfig() {
	cfg.UserKey = normalizeKey(cfg.UserKey)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fatal(err)
	}
	err = os.WriteFile(getConfigPath(), data, 0644)
	if err != nil {
		fatal(err)
	}
}

func checkKey(key string) error {
	key = normalizeKey(key)

	_, err := protocol.UserKeyFromString(key)
	if err != nil {
		return err
	}

	url, _ := url.JoinPath(cfg.ServerURL, "api/user")
	var response struct {
		Error *string `json:"error,omitempty"`
	}
	err = apiRequest(http.MethodGet, url, key, nil, &response)
	if err != nil {
		return err
	}

	if response.Error != nil {
		return errors.New(*response.Error)
	}

	return nil
}

func normalizeKey(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
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
	msgBox(nil, title, style, format, args...)
}

func msgBox(
	owner walk.Form,
	title string,
	style walk.MsgBoxStyle,
	format string,
	args ...interface{},
) {
	if owner == nil {
		owner = walk.App().ActiveForm()
		if owner == nil {
			owner = getAndShowMainWindow()
		}
	}

	var icon *walk.Icon
	switch style {
	case walk.MsgBoxIconInformation:
		icon = walk.IconInformation()
	case walk.MsgBoxIconError:
		icon = walk.IconError()
	case walk.MsgBoxIconWarning:
		icon = walk.IconWarning()
	default:
		fatal(fmt.Errorf("unknown message box style: %v", style))
	}

	var btnOk *walk.PushButton
	var dlg *walk.Dialog
	err := dec.Dialog{
		AssignTo:      &dlg,
		Title:         title,
		Icon:          icon,
		Font:          dec.Font{PointSize: walk.IntFrom96DPI(10, 96)},
		CancelButton:  &btnOk,
		DefaultButton: &btnOk,
		Layout:        dec.VBox{},
		Children: []dec.Widget{
			dec.LinkLabel{
				OnLinkActivated: onLinkActivated,
				MaxSize:         dec.Size{Width: 300},
				Text:            fmt.Sprintf(format, args...),
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
	}.Create(owner)
	if err != nil {
		// Fallback to message box.
		walk.MsgBox(owner, title, fmt.Sprintf(format, args...), style)
		return
	}

	_ = dlg.Run()
}

func onLinkActivated(link *walk.LinkLabelLink) {
	win.ShellExecute(mainWnd.Handle(),
		syscall.StringToUTF16Ptr("open"),
		syscall.StringToUTF16Ptr(link.URL()),
		nil, nil, win.SW_SHOWNORMAL,
	)
}

func apiRequest(method, url string, authKey string, params, response any) error {
	const timeout = 5 * time.Second

	var reader io.Reader
	if params != nil {
		requestData, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reader = bytes.NewReader(requestData)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if authKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authKey))
	}
	req.Header.Set("Content-type", "application/json")

	hc := http.Client{
		Timeout: timeout,
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", http.StatusText(resp.StatusCode))
	}

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}
