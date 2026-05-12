package main

import (
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func buildMenu(app *App) *menu.Menu {
	appMenu := menu.NewMenu()
	appMenu.Append(menu.AppMenu())
	appMenu.Append(menu.EditMenu())

	custom := appMenu.AddSubmenu("ptxt")
	custom.AddText("Back", keys.CmdOrCtrl("["), func(_ *menu.CallbackData) {
		if app.ctx != nil {
			wailsruntime.WindowExecJS(app.ctx, `window.history.back()`)
		}
	})
	custom.AddText("Forward", keys.CmdOrCtrl("]"), func(_ *menu.CallbackData) {
		if app.ctx != nil {
			wailsruntime.WindowExecJS(app.ctx, `window.history.forward()`)
		}
	})
	custom.AddSeparator()
	custom.AddText("Reload", keys.CmdOrCtrl("r"), func(_ *menu.CallbackData) {
		if app.ctx != nil {
			app.resetSplashHandoff()
			wailsruntime.WindowReload(app.ctx)
		}
	})
	custom.AddText("Force Reload (Re-navigate)", keys.Combo("r", keys.CmdOrCtrlKey, keys.ShiftKey), func(_ *menu.CallbackData) {
		if app.ctx != nil {
			app.forceRenavigate(app.ctx)
		}
	})
	custom.AddSeparator()
	custom.AddText("Open Data Folder", nil, func(_ *menu.CallbackData) {
		app.openDataDir()
	})

	appMenu.Append(menu.WindowMenu())
	return appMenu
}
