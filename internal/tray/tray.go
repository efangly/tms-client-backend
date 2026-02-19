package tray

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows/registry"
)

var (
	onExitCallback func()
	serverPort     string
)

// Run starts the system tray application. This blocks the main goroutine.
// startServer is called when the tray is ready, and should start the actual server in a goroutine.
// cleanup is called when the user clicks "Exit" from tray.
func Run(port string, startServer func(), cleanup func()) {
	serverPort = port
	onExitCallback = cleanup
	systray.Run(func() {
		onReady(startServer)
	}, onExit)
}

func onReady(startServer func()) {
	systray.SetIcon(GreenIcon())
	systray.SetTitle("TMS Backend")
	systray.SetTooltip(fmt.Sprintf("TMS Backend Server - Port %s", serverPort))

	mStatus := systray.AddMenuItem("TMS Backend - Running", "Server status")
	mStatus.Disable()

	systray.AddSeparator()

	mOpenBrowser := systray.AddMenuItem("Open Health Check", "Open browser to health check endpoint")
	mConsole := systray.AddMenuItem("Show Console", "Show/hide the console log window")
	mOpenLogs := systray.AddMenuItem("Open Error Logs", "Open the error logs directory")

	systray.AddSeparator()

	// Startup menu item
	mStartup := systray.AddMenuItem("Run at Windows Startup", "Configure to run when Windows starts")
	if IsInStartup() {
		mStartup.Check()
	}

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Exit", "Quit the application")

	// Start the actual server
	go startServer()

	// Handle menu item clicks
	go func() {
		for {
			select {
			case <-mOpenBrowser.ClickedCh:
				openURL(fmt.Sprintf("http://localhost:%s/health", serverPort))
			case <-mConsole.ClickedCh:
				if IsConsoleVisible() {
					HideConsole()
					mConsole.SetTitle("Show Console")
				} else {
					ShowConsole()
					mConsole.SetTitle("Hide Console")
				}
			case <-mOpenLogs.ClickedCh:
				openLogsFolder()
			case <-mStartup.ClickedCh:
				if mStartup.Checked() {
					if err := RemoveFromStartup(); err != nil {
						log.Printf("Failed to remove from startup: %v", err)
					} else {
						mStartup.Uncheck()
						log.Println("Removed from Windows startup")
					}
				} else {
					if err := AddToStartup(); err != nil {
						log.Printf("Failed to add to startup: %v", err)
					} else {
						mStartup.Check()
						log.Println("Added to Windows startup")
					}
				}
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	log.Println("System tray exit requested, shutting down...")
	if onExitCallback != nil {
		onExitCallback()
	}
}

// SetError updates the tray icon to indicate an error state
func SetError(msg string) {
	systray.SetIcon(RedIcon())
	systray.SetTooltip(fmt.Sprintf("TMS Backend - ERROR: %s", msg))
}

// SetRunning updates the tray icon to indicate running state
func SetRunning() {
	systray.SetIcon(GreenIcon())
	systray.SetTooltip(fmt.Sprintf("TMS Backend Server - Port %s", serverPort))
}

func openURL(url string) {
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		log.Printf("Failed to open URL: %v", err)
	}
}

func openLogsFolder() {
	logsDir := "logs"
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		os.MkdirAll(logsDir, 0755)
	}

	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("explorer", logsDir).Start()
	case "darwin":
		err = exec.Command("open", logsDir).Start()
	default:
		err = exec.Command("xdg-open", logsDir).Start()
	}
	if err != nil {
		log.Printf("Failed to open logs folder: %v", err)
	}
}

// Windows Startup Management
const startupRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const appName = "TMS-Backend"

// AddToStartup adds the application to Windows startup registry
func AddToStartup() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("startup registry is only supported on Windows")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open registry key: %w", err)
	}
	defer k.Close()

	// Quote the path to handle directory names with spaces
	if err := k.SetStringValue(appName, `"`+exePath+`"`); err != nil {
		return fmt.Errorf("failed to set registry value: %w", err)
	}

	return nil
}

// RemoveFromStartup removes the application from Windows startup registry
func RemoveFromStartup() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("startup registry is only supported on Windows")
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open registry key: %w", err)
	}
	defer k.Close()

	if err := k.DeleteValue(appName); err != nil {
		// If the value doesn't exist, it's not an error
		if err != registry.ErrNotExist {
			return fmt.Errorf("failed to delete registry value: %w", err)
		}
	}

	return nil
}

// IsInStartup checks if the application is in Windows startup
func IsInStartup() bool {
	if runtime.GOOS != "windows" {
		return false
	}

	k, err := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	_, _, err = k.GetStringValue(appName)
	return err == nil
}
