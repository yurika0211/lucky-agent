package tool

import "fmt"

// RequestCredentialTool asks the host to collect a secret through a masked
// form and store it in the local credential vault. The agent loop intercepts
// this name before the handler runs, so the model never sees the value.
func RequestCredentialTool() *Tool {
	return &Tool{
		Name:        "request_credential",
		Description: "Ask the user to fill a masked credential form and save the secret in the local encrypted vault. Use this for API keys, tokens, and passwords. Do not ask the user to paste a secret into chat, and do not pass the secret as a tool argument. The result contains only credential_ref.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"id":     {Type: "string", Description: "Stable credential reference, such as openai-main. Letters, numbers, dot, underscore, and hyphen only.", Required: true},
			"kind":   {Type: "string", Description: "Credential type. Use llm_api_key for model keys. Defaults to generic.", Required: false, Default: "generic"},
			"scope":  {Type: "string", Description: "Vault scope. Defaults to default.", Required: false, Default: "default"},
			"prompt": {Type: "string", Description: "Short label shown above the masked field, without asking the user to reveal the value in chat.", Required: true},
			"bind":   {Type: "string", Description: "Optional model endpoint to point at this credential after save: chat, compact, vision, image, transcription, tts, or embedding.", Required: false},
		},
		Handler: func(args map[string]any) (string, error) {
			return "", fmt.Errorf("request_credential must be handled by the agent runtime")
		},
	}
}
