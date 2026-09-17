//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	browser "pappice/demo/browser/app"
)

func main() {
	app, err := browser.New(js.Global().Get("pappicePageURL").String())
	if err != nil {
		js.Global().Call("postMessage", map[string]any{"error": err.Error()})
		return
	}
	defer app.Close()
	request := js.FuncOf(func(_ js.Value, args []js.Value) any {
		id, input := args[0].Int(), args[1].String()
		// Yield to Go's scheduler so SQL waits cannot block the JS callback.
		go func() {
			reply := map[string]any{"id": id}
			defer func() {
				if err := recover(); err != nil {
					reply["error"] = fmt.Sprint(err)
				}
				js.Global().Call("postMessage", reply)
			}()
			var req browser.Request
			if err := json.Unmarshal([]byte(input), &req); err != nil {
				reply["error"] = err.Error()
				return
			}
			response, err := json.Marshal(app.Request(req))
			if err != nil {
				reply["error"] = err.Error()
				return
			}
			reply["response"] = string(response)
		}()
		return nil
	})
	defer request.Release()
	js.Global().Set("pappiceRequest", request)
	js.Global().Call("postMessage", map[string]any{"ready": true})
	select {}
}
