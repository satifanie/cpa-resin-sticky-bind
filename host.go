package main

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func hostLog(level, message string) {
	payload, _ := json.Marshal(map[string]any{
		"level":   level,
		"message": message,
	})
	_, _ = callHostRaw(pluginabi.MethodHostLog, payload)
}

func callHostRaw(method string, request []byte) ([]byte, error) {
	return hostCall(method, request)
}

var hostCall = func(method string, request []byte) ([]byte, error) {
	return nil, fmt.Errorf("host call unavailable")
}

func callHostResult(method string, payload any) (json.RawMessage, error) {
	rawPayload, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal host payload %s: %w", method, errMarshal)
	}
	raw, errCall := callHostRaw(method, rawPayload)
	if errCall != nil {
		return nil, errCall
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode host envelope %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("host %s: %s: %s", method, env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host %s failed", method)
	}
	return append(json.RawMessage(nil), env.Result...), nil
}
