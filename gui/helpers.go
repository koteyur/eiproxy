package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	dec "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

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

func msgBox(
	owner walk.Form,
	title string,
	style walk.MsgBoxStyle,
	format string,
	args ...interface{},
) {
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

func getExeDir() string {
	exePath, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	return filepath.Dir(exePath)
}
