package tray

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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

// Windows Startup Management via HKCU Registry Run key + VBScript launcher.
//
// Why VBScript wrapper instead of registering the exe directly?
//   - The HKCU Run key fires very early in the Windows session, before many
//     environment variables (TEMP, USERPROFILE, etc.) are fully initialised.
//     A windowsgui exe launched at that moment often silently exits with no log.
//   - wscript.exe runs the VBS in a fully-initialised user session, giving the
//     real exe a stable environment and the correct working directory.
//   - This is the same technique used by PM2-windows-startup and other tools.
const runKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
const taskName = "TMS-Backend"

// vbsLauncherName is placed next to the exe.
const vbsLauncherName = "tms-backend-startup.vbs"

// vbsTemplate launches the exe silently from its own directory.
// CreateObject("WScript.Shell").Run wraps the launch so the working directory
// is set correctly and wscript.exe returns immediately (intWindowStyle=0, bWaitOnReturn=false).
const vbsTemplate = `Set oShell = CreateObject("WScript.Shell")
oShell.CurrentDirectory = "%s"
oShell.Run """%s""", 0, False
`

// AddToStartup writes a VBScript launcher next to the exe and registers it
// in the HKCU Run key so Windows starts the app silently at logon.
func AddToStartup() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("only supported on Windows")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	vbsPath := filepath.Join(exeDir, vbsLauncherName)

	// Write the VBScript launcher
	vbsContent := fmt.Sprintf(vbsTemplate, exeDir, exePath)
	if err := os.WriteFile(vbsPath, []byte(vbsContent), 0644); err != nil {
		return fmt.Errorf("failed to write VBS launcher: %w", err)
	}

	// Use the registry package to write the correct value with embedded quotes.
	// reg.exe cannot reliably store `wscript.exe "path"` (embedded quotes get dropped).
	k, err := registry.OpenKey(registry.CURRENT_USER, strings.TrimPrefix(runKeyPath, `HKCU\`), registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open registry key: %w", err)
	}
	defer k.Close()

	value := `wscript.exe "` + vbsPath + `"`
	if err := k.SetStringValue(taskName, value); err != nil {
		return fmt.Errorf("failed to set registry value: %w", err)
	}

	log.Printf("Startup registered via VBS: %s", value)
	return nil
}

// RemoveFromStartup removes the Run key entry and the VBScript launcher.
func RemoveFromStartup() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("only supported on Windows")
	}

	cmd := exec.Command("reg", "delete", runKeyPath, "/v", taskName, "/f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(out), "unable to find") && !strings.Contains(string(out), "does not exist") {
			return fmt.Errorf("reg delete failed: %v\nOutput: %s", err, string(out))
		}
	}

	// Also clean up the VBS file
	exePath, _ := os.Executable()
	exePath, _ = filepath.Abs(exePath)
	vbsPath := filepath.Join(filepath.Dir(exePath), vbsLauncherName)
	os.Remove(vbsPath) // best-effort

	log.Println("Startup removed")
	return nil
}

// IsInStartup reports whether the HKCU Run entry exists.
func IsInStartup() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	cmd := exec.Command("reg", "query", runKeyPath, "/v", taskName)
	return cmd.Run() == nil
}
