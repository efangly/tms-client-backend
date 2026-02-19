package tray

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32DLL              = syscall.NewLazyDLL("kernel32.dll")
	user32DLL                = syscall.NewLazyDLL("user32.dll")
	procAllocConsole         = kernel32DLL.NewProc("AllocConsole")
	procGetConsoleWindow     = kernel32DLL.NewProc("GetConsoleWindow")
	procSetConsoleTitleW     = kernel32DLL.NewProc("SetConsoleTitleW")
	procShowWindow           = user32DLL.NewProc("ShowWindow")
	procIsWindowVisible      = user32DLL.NewProc("IsWindowVisible")
	procGetSystemMenu        = user32DLL.NewProc("GetSystemMenu")
	procDeleteMenu           = user32DLL.NewProc("DeleteMenu")
	procSetConsoleBufferSize = kernel32DLL.NewProc("SetConsoleScreenBufferSize")

	consoleHwnd uintptr
	consoleOnce sync.Once
	consoleMu   sync.Mutex
)

const (
	swHide      = 0
	swShow      = 5
	scClose     = 0xF060
	mfByCommand = 0x0
)

// InitConsole allocates a hidden Windows console for log output.
// The console can be shown/hidden via the system tray menu.
func InitConsole() error {
	var initErr error
	consoleOnce.Do(func() {
		r, _, err := procAllocConsole.Call()
		if r == 0 {
			initErr = fmt.Errorf("AllocConsole failed: %v", err)
			return
		}

		// Set console title
		title, _ := syscall.UTF16PtrFromString("TMS Backend - Console Log")
		procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(title)))

		// Open CONOUT$ to redirect output to the new console
		conout, err2 := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
		if err2 != nil {
			initErr = fmt.Errorf("failed to open CONOUT$: %v", err2)
			return
		}

		// Redirect Go standard output and logger to console
		os.Stdout = conout
		os.Stderr = conout
		log.SetOutput(conout)

		// Set a larger scroll buffer (120 columns x 9999 rows)
		stdoutHandle, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
		bufferSize := uintptr(120) | (uintptr(9999) << 16)
		procSetConsoleBufferSize.Call(uintptr(stdoutHandle), bufferSize)

		// Get console window handle
		consoleHwnd, _, _ = procGetConsoleWindow.Call()

		// Disable the close (X) button to prevent accidental process termination
		hmenu, _, _ := procGetSystemMenu.Call(consoleHwnd, 0)
		if hmenu != 0 {
			procDeleteMenu.Call(hmenu, scClose, mfByCommand)
		}

		// Hide console initially
		procShowWindow.Call(consoleHwnd, swHide)

		// Print local IP addresses for reference
		printLocalIPs()
	})
	return initErr
}

// ShowConsole makes the console window visible
func ShowConsole() {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	if consoleHwnd != 0 {
		procShowWindow.Call(consoleHwnd, swShow)
	}
}

// HideConsole hides the console window
func HideConsole() {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	if consoleHwnd != 0 {
		procShowWindow.Call(consoleHwnd, swHide)
	}
}

// IsConsoleVisible returns true if the console window is currently visible
func IsConsoleVisible() bool {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	if consoleHwnd == 0 {
		return false
	}
	ret, _, _ := procIsWindowVisible.Call(consoleHwnd)
	return ret != 0
}

func printLocalIPs() {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				log.Printf("Local IP: %s", ipnet.IP)
			}
		}
	}
}
