package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	appTitle = "OpenMAIC 本地启动器"
	port     = "3000"
	model    = "deepseek:deepseek-v4-flash"
)

var mu sync.Mutex
var server *exec.Cmd
var serverLog *os.File
var editHwnd uintptr
var statusHwnd uintptr
var titleHwnd uintptr
var instance uintptr
var mainWindow uintptr
var running bool

// Windows API declarations.
var (
	registerClassExW     = uintptr(0)
	createWindowExW      = uintptr(0)
	defWindowProcW       = uintptr(0)
	getMessageW          = uintptr(0)
	translateMessage     = uintptr(0)
	dispatchMessageW     = uintptr(0)
	postQuitMessage      = uintptr(0)
	showWindow           = uintptr(0)
	updateWindow         = uintptr(0)
	setWindowTextW       = uintptr(0)
	getWindowTextW       = uintptr(0)
	getWindowTextLengthW = uintptr(0)
	sendMessageW         = uintptr(0)
	messageBoxW          = uintptr(0)
	loadCursorW          = uintptr(0)
	getModuleHandleW     = uintptr(0)
	createFontW          = uintptr(0)
	dpapiProtectData     = uintptr(0)
	dpapiUnprotectData   = uintptr(0)
	localFree            = uintptr(0)
	shellExecuteW        = uintptr(0)
)

const (
	wmDestroy                      = 0x0002
	wmClose                        = 0x0010
	wmCommand                      = 0x0111
	wmSetFont                      = 0x0030
	wmGetText                      = 0x000D
	wmGetTextLen                   = 0x000E
	wmSetText                      = 0x000C
	wmEraseBkgnd                   = 0x0014
	bsPushButton                   = 0x00000000
	esAutoHScroll                  = 0x0080
	esPassword                     = 0x0020
	wsChild                        = 0x40000000
	wsVisible                      = 0x10000000
	wsBorder                       = 0x00800000
	wsCaption                      = 0x00C00000
	wsSysMenu                      = 0x00080000
	wsMinimizeBox                  = 0x00020000
	wsOverlapped                   = 0x00000000
	swShow                         = 5
	idKeyEdit                      = 101
	idSave                         = 102
	idStart                        = 103
	idStop                         = 104
	idOpen                         = 105
	idStatus                       = 106
	mbOK                           = 0x00000000
	mbIconWarn                     = 0x00000030
	mbIconError                    = 0x00000010
	mbIconInfo                     = 0x00000040
	cwUseDefault                   = 0x80000000
	colorWindow                    = 5
	colorBtnFace                   = 15
	colorStatic                    = 6
	fontNormal                     = 400
	fontSemiBold                   = 600
	defaultCharSet                 = 1
	defaultOutPrecision            = 0
	defaultClipPrecision           = 0
	cleartypeQuality               = 5
	defaultPitch                   = 0
	fontDontCare                   = 0
	swpNoSize                      = 0x0001
	swpNoZOrder                    = 0x0004
	swpNoActivate                  = 0x0010
	processQueryLimitedInformation = 0x1000
	synchronize                    = 0x00100000
	waitObject0                    = 0
	waitTimeout                    = 258
	stillActive                    = 259
	startupInfoSize                = 104
)

type point struct{ X, Y int32 }
type msg struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
}
type wndClassEx struct {
	Size                               uint32
	Style                              uint32
	WndProc                            uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type dataBlob struct {
	Size uint32
	Data *byte
}

type wndProcType func(uintptr, uint32, uintptr, uintptr) uintptr

var wndProcPointer = windows.NewCallback(windowProc)

func loadAPI() error {
	// Go's Windows syscall package provides reliable lazy DLL loading.
	user := windows.NewLazyDLL("user32.dll")
	kernel := windows.NewLazyDLL("kernel32.dll")
	crypt := windows.NewLazyDLL("crypt32.dll")
	shell := windows.NewLazyDLL("shell32.dll")
	gdi := windows.NewLazyDLL("gdi32.dll")
	for _, d := range []*windows.LazyDLL{user, kernel, crypt, shell, gdi} {
		if err := d.Load(); err != nil {
			return err
		}
	}
	get := func(d *windows.LazyDLL, n string) uintptr {
		p := d.NewProc(n)
		if e := p.Find(); e != nil {
			panic(e)
		}
		syscallProcs[p.Addr()] = p
		return p.Addr()
	}
	registerClassExW = get(user, "RegisterClassExW")
	createWindowExW = get(user, "CreateWindowExW")
	defWindowProcW = get(user, "DefWindowProcW")
	getMessageW = get(user, "GetMessageW")
	translateMessage = get(user, "TranslateMessage")
	dispatchMessageW = get(user, "DispatchMessageW")
	postQuitMessage = get(user, "PostQuitMessage")
	showWindow = get(user, "ShowWindow")
	updateWindow = get(user, "UpdateWindow")
	setWindowTextW = get(user, "SetWindowTextW")
	getWindowTextW = get(user, "GetWindowTextW")
	getWindowTextLengthW = get(user, "GetWindowTextLengthW")
	sendMessageW = get(user, "SendMessageW")
	messageBoxW = get(user, "MessageBoxW")
	loadCursorW = get(user, "LoadCursorW")
	getModuleHandleW = get(kernel, "GetModuleHandleW")
	createFontW = get(gdi, "CreateFontW")
	dpapiProtectData = get(crypt, "CryptProtectData")
	dpapiUnprotectData = get(crypt, "CryptUnprotectData")
	localFree = get(kernel, "LocalFree")
	shellExecuteW = get(shell, "ShellExecuteW")
	return nil
}

func utf16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

var syscallProcs = map[uintptr]*windows.LazyProc{}

func call(p uintptr, args ...uintptr) (uintptr, uintptr, windows.Errno) {
	proc := syscallProcs[p]
	if proc == nil {
		return 0, 0, windows.ERROR_PROC_NOT_FOUND
	}
	var r uintptr
	var e error
	switch len(args) {
	case 0:
		r, _, e = proc.Call()
	case 1:
		r, _, e = proc.Call(args[0])
	case 2:
		r, _, e = proc.Call(args[0], args[1])
	case 3:
		r, _, e = proc.Call(args[0], args[1], args[2])
	case 4:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3])
	case 5:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4])
	case 6:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5])
	case 7:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6])
	case 8:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7])
	case 9:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8])
	case 10:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9])
	case 11:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10])
	case 12:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], args[11])
	case 13:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], args[11], args[12])
	case 14:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], args[11], args[12], args[13])
	case 15:
		r, _, e = proc.Call(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], args[11], args[12], args[13], args[14])
	default:
		panic("unsupported Windows call arity")
	}
	if e != nil {
		if errno, ok := e.(windows.Errno); ok {
			return r, 0, errno
		}
	}
	return r, 0, 0
}
func handleErr(r uintptr, e windows.Errno) error {
	if r == 0 {
		if e != 0 {
			return e
		}
		return windows.ERROR_INVALID_FUNCTION
	}
	return nil
}

func setText(hwnd uintptr, text string) {
	call(setWindowTextW, hwnd, uintptr(unsafe.Pointer(utf16(text))))
}
func text(hwnd uintptr) string {
	n, _, _ := call(getWindowTextLengthW, hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	call(getWindowTextW, hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}
func message(hwnd uintptr, title, body string, flags uintptr) {
	call(messageBoxW, hwnd, uintptr(unsafe.Pointer(utf16(body))), uintptr(unsafe.Pointer(utf16(title))), flags)
}
func createControl(parent uintptr, class, label string, style uintptr, x, y, w, h int32, id uintptr) uintptr {
	c := utf16(class)
	t := utf16(label)
	r, _, e := call(createWindowExW, 0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), style|wsChild|wsVisible, uintptr(uint32(x)), uintptr(uint32(y)), uintptr(uint32(w)), uintptr(uint32(h)), parent, id, instance, 0)
	if r == 0 {
		panic(fmt.Sprintf("CreateWindowExW %s: %v", class, e))
	}
	return r
}
func fontFor(hwnd uintptr, size int32, weight int32) {
	face := utf16("Microsoft YaHei UI")
	r, _, _ := call(createFontW, uintptr(uint32(-size)), 0, 0, 0, uintptr(weight), 0, 0, 0, defaultCharSet, defaultOutPrecision, defaultClipPrecision, cleartypeQuality, defaultPitch|fontDontCare, uintptr(unsafe.Pointer(face)))
	if r != 0 {
		call(sendMessageW, hwnd, wmSetFont, r, 1)
	}
}

func windowProc(hwnd uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case wmCommand:
		switch uint16(w & 0xffff) {
		case idSave:
			saveKey()
		case idStart:
			startService()
		case idStop:
			stopService()
		case idOpen:
			openBrowser()
		}
		return 0
	case wmClose:
		stopService()
		call(postQuitMessage, 0)
		return 0
	case wmDestroy:
		call(postQuitMessage, 0)
		return 0
	}
	r, _, _ := call(defWindowProcW, hwnd, uintptr(m), w, l)
	return r
}

func createWindow() error {
	mod, _, _ := call(getModuleHandleW, 0)
	instance = mod
	className := utf16("OpenMAICLauncherWindow")
	cursor, _, _ := call(loadCursorW, 0, uintptr(32512))
	wc := wndClassEx{Size: uint32(unsafe.Sizeof(wndClassEx{})), WndProc: wndProcPointer, Instance: mod, Cursor: cursor, Background: colorWindow, ClassName: className}
	r, _, e := call(registerClassExW, uintptr(unsafe.Pointer(&wc)))
	if r == 0 && e != windows.Errno(1410) {
		return fmt.Errorf("RegisterClassExW: %v", e)
	}
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox)
	mainWindow, _, e = call(createWindowExW, 0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(utf16(appTitle))), style, cwUseDefault, cwUseDefault, 600, 430, 0, 0, mod, 0)
	if mainWindow == 0 {
		return fmt.Errorf("CreateWindowExW: %v", e)
	}
	titleHwnd = createControl(mainWindow, "STATIC", "OpenMAIC", wsVisible, 28, 20, 500, 38, 1)
	createControl(mainWindow, "STATIC", "本机运行 · API Key 只保存在这台电脑", wsVisible, 30, 59, 520, 24, 2)
	createControl(mainWindow, "STATIC", "DeepSeek API Key", wsVisible, 30, 108, 180, 24, 3)
	editHwnd = createControl(mainWindow, "EDIT", "", wsBorder|esAutoHScroll|esPassword, 30, 139, 405, 32, idKeyEdit)
	createControl(mainWindow, "BUTTON", "本机保存", bsPushButton, 447, 138, 112, 34, idSave)
	statusHwnd = createControl(mainWindow, "STATIC", "首次启动请填写 API Key。不会上传给启动器开发者。", wsVisible, 30, 184, 530, 48, idStatus)
	createControl(mainWindow, "BUTTON", "启动 OpenMAIC", bsPushButton, 30, 246, 165, 42, idStart)
	createControl(mainWindow, "BUTTON", "停止服务", bsPushButton, 208, 246, 112, 42, idStop)
	createControl(mainWindow, "BUTTON", "打开课堂", bsPushButton, 333, 246, 120, 42, idOpen)
	createControl(mainWindow, "STATIC", "课堂数据默认留在当前浏览器。生成内容由 DeepSeek API 按其规则计费。", wsVisible, 30, 316, 530, 42, 9)
	createControl(mainWindow, "STATIC", "日志：%LOCALAPPDATA%\\OpenMAIC\\openmaic.log", wsVisible, 30, 365, 530, 22, 10)
	for _, h := range []uintptr{editHwnd, statusHwnd} {
		fontFor(h, 10, fontNormal)
	}
	fontFor(titleHwnd, 21, fontSemiBold)
	if _, err := os.Stat(keyPath()); err == nil {
		setText(statusHwnd, "检测到本机加密保存的 Key；如需更换，请输入新 Key 后点击“本机保存”。")
	}
	call(showWindow, mainWindow, swShow)
	call(updateWindow, mainWindow)
	return nil
}
func baseDir() string {
	exe, e := os.Executable()
	if e != nil {
		return "."
	}
	return filepath.Dir(exe)
}
func runtimeDir() string { return filepath.Join(baseDir(), "runtime") }
func dataDir() string {
	d := os.Getenv("LOCALAPPDATA")
	if d == "" {
		d = os.TempDir()
	}
	p := filepath.Join(d, "OpenMAIC")
	_ = os.MkdirAll(p, 0700)
	return p
}
func keyPath() string    { return filepath.Join(dataDir(), "deepseek-key.dpapi") }
func pidPath() string    { return filepath.Join(dataDir(), "openmaic.pid") }
func logPath() string    { return filepath.Join(dataDir(), "openmaic.log") }
func endpoint() string   { return "http://127.0.0.1:" + port }
func setStatus(s string) { setText(statusHwnd, s) }

func blob(data []byte) dataBlob {
	if len(data) == 0 {
		return dataBlob{}
	}
	return dataBlob{Size: uint32(len(data)), Data: &data[0]}
}
func protect(data []byte) ([]byte, error) {
	in := blob(data)
	var out dataBlob
	r, _, e := call(dpapiProtectData, uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, e
	}
	defer call(localFree, uintptr(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func unprotect(data []byte) ([]byte, error) {
	in := blob(data)
	var out dataBlob
	r, _, e := call(dpapiUnprotectData, uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, e
	}
	defer call(localFree, uintptr(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
func saveKey() {
	value := strings.TrimSpace(text(editHwnd))
	if len(value) < 8 {
		message(mainWindow, appTitle, "API Key 看起来太短，请检查后再保存。", mbOK|mbIconWarn)
		return
	}
	plain := []byte(value)
	enc, err := protect(plain)
	clear(plain)
	value = ""
	if err != nil {
		message(mainWindow, appTitle, "Windows DPAPI 加密失败："+err.Error(), mbOK|mbIconError)
		return
	}
	if err = os.WriteFile(keyPath(), []byte(hex.EncodeToString(enc)), 0600); err != nil {
		message(mainWindow, appTitle, "无法保存密钥："+err.Error(), mbOK|mbIconError)
		return
	}
	call(setWindowTextW, editHwnd, uintptr(unsafe.Pointer(utf16(""))))
	setStatus("API Key 已使用当前 Windows 账户的 DPAPI 加密保存在本机。")
}
func loadKey() (string, error) {
	b, e := os.ReadFile(keyPath())
	if e != nil {
		return "", e
	}
	raw, e := hex.DecodeString(strings.TrimSpace(string(b)))
	if e != nil {
		return "", e
	}
	plain, e := unprotect(raw)
	clear(raw)
	if e != nil {
		return "", e
	}
	s := string(plain)
	clear(plain)
	return s, nil
}
func clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func startService() {
	mu.Lock()
	defer mu.Unlock()
	if running {
		setStatus("OpenMAIC 已在运行。等待健康检查…")
		return
	}
	node := filepath.Join(runtimeDir(), "node.exe")
	serverJS := filepath.Join(runtimeDir(), "server.js")
	for _, p := range []string{node, serverJS, filepath.Join(runtimeDir(), ".next", "static")} {
		if _, e := os.Stat(p); e != nil {
			message(mainWindow, appTitle, "发行文件不完整，缺少："+p, mbOK|mbIconError)
			return
		}
	}
	key, e := loadKey()
	if e != nil {
		entered := text(editHwnd)
		if len(entered) < 8 {
			setStatus("请先填写 DeepSeek API Key，再点“启动 OpenMAIC”。")
			return
		}
		if err := writeKey(entered); err != nil {
			message(mainWindow, appTitle, "Key 加密保存失败："+err.Error(), mbOK|mbIconError)
			clear([]byte(entered))
			return
		}
		key = entered
		call(setWindowTextW, editHwnd, uintptr(unsafe.Pointer(utf16(""))))
	}
	log, e := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		message(mainWindow, appTitle, "无法创建本地日志："+e.Error(), mbOK|mbIconError)
		clear([]byte(key))
		return
	}
	cmd := exec.Command(node, "server.js")
	cmd.Dir = runtimeDir()
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = childEnvironment(key)
	e = cmd.Start()
	clear([]byte(key))
	key = ""
	if e != nil {
		log.Close()
		setStatus("启动 Node 服务失败：" + e.Error())
		return
	}
	server = cmd
	serverLog = log
	running = true
	_ = os.WriteFile(pidPath(), []byte(fmt.Sprint(cmd.Process.Pid)), 0600)
	setStatus("正在启动…服务正常后会自动打开浏览器。API Key 由本机 Node 进程使用。")
	go func() {
		err := cmd.Wait()
		mu.Lock()
		defer mu.Unlock()
		running = false
		server = nil
		if serverLog != nil {
			serverLog.Close()
			serverLog = nil
		}
		_ = os.Remove(pidPath())
		if err != nil {
			setStatus("服务已退出；请检查本机 openmaic.log。")
		}
	}()
	go waitForHealth(cmd.Process.Pid)
}
func writeKey(value string) error {
	enc, e := protect([]byte(value))
	clear([]byte(value))
	if e != nil {
		return e
	}
	defer clear(enc)
	return os.WriteFile(keyPath(), []byte(hex.EncodeToString(enc)), 0600)
}
func childEnvironment(key string) []string {
	return buildChildEnvironment(os.Environ(), key)
}

func waitForHealth(pid int) {
	client := http.Client{Timeout: 1200 * time.Millisecond}
	deadline := time.Now().Add(100 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		alive := running && server != nil && server.Process.Pid == pid
		mu.Unlock()
		if !alive {
			return
		}
		r, e := client.Get(endpoint() + "/api/health")
		if e == nil {
			var body struct {
				Status string `json:"status"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&body)
			r.Body.Close()
			if r.StatusCode == 200 && body.Status == "ok" {
				setStatus("OpenMAIC 已就绪，正在打开本机课堂…")
				openBrowser()
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	setStatus("启动等待超时；查看 %LOCALAPPDATA%\\OpenMAIC\\openmaic.log")
}
func stopService() {
	mu.Lock()
	cmd := server
	log := serverLog
	server = nil
	running = false
	serverLog = nil
	mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if log != nil {
		_ = log.Close()
	}
	_ = os.Remove(pidPath())
	setStatus("OpenMAIC 服务已停止。")
}

func openBrowser() {
	url := utf16(endpoint())
	r, _, e := call(shellExecuteW, mainWindow, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(url)), 0, 0, swShow)
	if r <= 32 {
		message(mainWindow, appTitle, fmt.Sprintf("无法打开浏览器（错误 %d / %v）。请访问 %s", r, e, endpoint()), mbOK|mbIconWarn)
	}
}
func pollServer() {
	client := http.Client{Timeout: 800 * time.Millisecond}
	for {
		time.Sleep(2 * time.Second)
		mu.Lock()
		alive := running && server != nil
		mu.Unlock()
		if !alive {
			continue
		}
		r, err := client.Get(endpoint() + "/api/health")
		if err != nil {
			setStatus("服务正在启动…请稍候。")
			continue
		}
		var body struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&body)
		r.Body.Close()
		if r.StatusCode == http.StatusOK && body.Status == "ok" {
			setStatus("OpenMAIC 已就绪，正在打开本机课堂…")
			openBrowser()
			return
		}
	}
}

func main() {
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "Windows only")
		os.Exit(1)
	}
	if err := loadAPI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := createWindow(); err != nil {
		message(0, appTitle, err.Error(), mbOK|mbIconError)
		os.Exit(1)
	}
	go pollServer()
	var m msg
	for {
		r, _, e := call(getMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == 0 {
			break
		}
		if int32(r) == -1 {
			fmt.Fprintln(os.Stderr, "GetMessageW:", e)
			break
		}
		call(translateMessage, uintptr(unsafe.Pointer(&m)))
		call(dispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
}
