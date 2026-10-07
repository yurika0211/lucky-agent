package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/credentials"
)

const hitlKindCredential = "credential"

// executeRequestCredentialHITL shows a masked form, writes the submitted
// secret into the local vault, and returns only the credential reference.
func (a *Agent) executeRequestCredentialHITL(ctx context.Context, sessionID string, args map[string]any) (detailedToolExecutionResult, error) {
	if a == nil || a.hitl == nil {
		return detailedToolExecutionResult{}, fmt.Errorf("request_credential requires the runtime hitl gate")
	}
	fields, err := credentialFormFields(args)
	meta := credentialFormMeta(fields, err)
	if err != nil {
		return detailedToolExecutionResult{Metadata: meta}, err
	}
	res, err := a.hitl.RequestAndWait(ctx, hitlRequest{
		Kind:      hitlKindCredential,
		SessionID: sessionID,
		Tool:      "request_credential",
		Prompt:    fields.prompt,
		Reason:    fields.prompt,
		Summary:   "需要你在安全表单填写凭据: " + fields.id,
		Args: map[string]any{
			"id":     fields.id,
			"kind":   fields.kind,
			"scope":  fields.scope,
			"prompt": fields.prompt,
			"bind":   fields.bind,
		},
	}, hitlEmitterFrom(ctx))
	if err != nil {
		return detailedToolExecutionResult{Metadata: meta}, err
	}
	if isDenyDecision(res.Decision) || strings.TrimSpace(res.Input) == "" {
		return detailedToolExecutionResult{
			Output:   "User canceled the credential form. Secret was not stored.",
			Metadata: meta,
		}, nil
	}
	home := ""
	if a.cfg != nil {
		home = strings.TrimSpace(a.cfg.HomeDir())
	}
	if home == "" {
		return detailedToolExecutionResult{Metadata: meta}, fmt.Errorf("credential home directory is unavailable")
	}
	secret := []byte(res.Input)
	defer zeroCredentialBytes(secret)
	store, err := credentials.NewStore(home)
	if err != nil {
		return detailedToolExecutionResult{Metadata: meta}, err
	}
	defer store.Close()
	if err := store.PutBytes(ctx, fields.id, fields.kind, fields.scope, secret); err != nil {
		return detailedToolExecutionResult{Metadata: meta}, err
	}
	zeroCredentialBytes(secret)
	bound := ""
	if fields.bind != "" {
		if err := a.bindCredentialRef(fields.bind, fields.id); err != nil {
			return detailedToolExecutionResult{
				Output:   fmt.Sprintf("Credential saved as credential_ref=%s. Binding to %s failed: %s", fields.id, fields.bind, err.Error()),
				Metadata: meta,
			}, nil
		}
		bound = fields.bind
	}
	output := fmt.Sprintf("Credential saved. credential_ref=%s kind=%s scope=%s. The secret is not available to tools.", fields.id, fields.kind, fields.scope)
	if bound != "" {
		output += " Bound to models.endpoints." + bound + ".credential_ref."
	}
	return detailedToolExecutionResult{Output: output, Metadata: meta}, nil
}

type credentialFields struct {
	id     string
	kind   string
	scope  string
	prompt string
	bind   string
}

func credentialFormFields(args map[string]any) (credentialFields, error) {
	fields := credentialFields{kind: "generic", scope: "default"}
	if args != nil {
		fields.id, _ = args["id"].(string)
		if kind, _ := args["kind"].(string); strings.TrimSpace(kind) != "" {
			fields.kind = kind
		}
		if scope, _ := args["scope"].(string); strings.TrimSpace(scope) != "" {
			fields.scope = scope
		}
		fields.prompt, _ = args["prompt"].(string)
		if fields.prompt == "" {
			fields.prompt, _ = args["label"].(string)
		}
		fields.bind, _ = args["bind"].(string)
	}
	var err error
	fields.id, err = credentials.ValidateID(fields.id)
	if err != nil {
		return fields, err
	}
	fields.kind, err = credentials.ValidateKind(fields.kind)
	if err != nil {
		return fields, err
	}
	fields.scope, err = credentials.NormalizeScope(fields.scope)
	if err != nil {
		return fields, err
	}
	fields.prompt = strings.TrimSpace(fields.prompt)
	if fields.prompt == "" {
		fields.prompt = "填写凭据 " + fields.id
	}
	if len(fields.prompt) > 240 {
		fields.prompt = fields.prompt[:240]
	}
	fields.bind = strings.TrimSpace(fields.bind)
	if fields.bind != "" {
		kind, kindErr := config.ParseModelKind(fields.bind)
		if kindErr != nil {
			return fields, fmt.Errorf("bind must be a model endpoint kind: %w", kindErr)
		}
		fields.bind = string(kind)
	}
	return fields, nil
}

func credentialFormMeta(fields credentialFields, formErr error) map[string]any {
	form := map[string]any{
		"tool":            "request_credential",
		"action":          hitlKindCredential,
		"reason":          fields.prompt,
		"kind":            hitlKindCredential,
		"prompt":          fields.prompt,
		"secure":          true,
		"id":              fields.id,
		"credential_kind": fields.kind,
		"scope":           fields.scope,
	}
	if fields.bind != "" {
		form["bind"] = fields.bind
	}
	if formErr != nil {
		form["error"] = formErr.Error()
	}
	return map[string]any{"approval_required": form}
}

func (a *Agent) bindCredentialRef(kind, id string) error {
	if a == nil || a.cfg == nil {
		return fmt.Errorf("config manager is unavailable")
	}
	key := "models.endpoints." + kind + ".credential_ref"
	if err := a.cfg.Set(key, id); err != nil {
		return err
	}
	return a.cfg.Save()
}

func zeroCredentialBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
