package system

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

type Controller struct {
	osType string
}

func NewController() *Controller {
	return &Controller{
		osType: runtime.GOOS,
	}
}

func (c *Controller) GetOSName() string {
	return c.osType
}

func (c *Controller) ClickAcceptButton(x, y int) bool {
	var err error
	
	if c.osType == "windows" {
		err = c.clickWindows(x, y)
	} else {
		err = c.clickUnix(x, y)
	}
	
	return err == nil
}

func (c *Controller) clickWindows(x, y int) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Cursor]::Position = [System.Drawing.Point]::new(%d, %d)
Start-Sleep -Milliseconds 50
Add-Type -TypeDefinition '
using System;
using System.Runtime.InteropServices;
public class Mouse {
    [DllImport("user32.dll")]
    public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, IntPtr dwExtraInfo);
    public const uint MOUSEEVENTF_LEFTDOWN = 0x02;
    public const uint MOUSEEVENTF_LEFTUP = 0x04;
}
'
[Mouse]::mouse_event([Mouse]::MOUSEEVENTF_LEFTDOWN, 0, 0, 0, [IntPtr]::Zero)
Start-Sleep -Milliseconds 50
[Mouse]::mouse_event([Mouse]::MOUSEEVENTF_LEFTUP, 0, 0, 0, [IntPtr]::Zero)
`, x, y)

	cmd := exec.Command("powershell", "-Command", script)
	return cmd.Run()
}

func (c *Controller) clickUnix(x, y int) error {
	cmd := exec.Command("xdotool", "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	err := cmd.Run()
	if err != nil {
		return err
	}

	time.Sleep(50 * time.Millisecond)

	cmd = exec.Command("xdotool", "click", "1")
	return cmd.Run()
}

func (c *Controller) IsSystemSupported() bool {
	if c.osType == "windows" {
		return true
	} else {
		// xdotoolが利用可能かチェック
		cmd := exec.Command("which", "xdotool")
		err := cmd.Run()
		return err == nil
	}
}