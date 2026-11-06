// MyVpn desktop application (Wails). The privileged tunnel lives in the
// myvpnd service; this process only handles UI and the SSO browser login.
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "MyVpn",
		Width:            420,
		Height:           680,
		MinWidth:         380,
		MinHeight:        560,
		BackgroundColour: &options.RGBA{R: 11, G: 17, B: 32, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.myvpn.app",
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About:    &mac.AboutInfo{Title: "MyVpn", Message: "Cliente SSL VPN ligero para FortiGate con SSO."},
		},
		Windows: &windows.Options{Theme: windows.Dark},
	})
	if err != nil {
		log.Fatal(err)
	}
}
