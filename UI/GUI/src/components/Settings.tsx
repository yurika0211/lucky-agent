import { ChangeEvent, FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import type { ActivityNote } from '../types';

interface SettingsProps {
  fetchRuntime: (path: string, init?: RequestInit) => Promise<Response>;
  pushActivity: (kind: ActivityNote['kind'], title: string, body: string, meta?: string) => void;
}

type RuntimeConfig = Record<string, any>;

type ModelKind = 'chat' | 'vision' | 'embedding' | 'transcription' | 'image' | 'tts' | 'reranker';

const MODEL_KINDS: Array<{ id: ModelKind; label: string; detail: string }> = [
  { id: 'chat', label: 'Chat', detail: 'Agent reasoning and replies' },
  { id: 'vision', label: 'Vision', detail: 'Image understanding when the chat model cannot see images' },
  { id: 'embedding', label: 'Embedding', detail: 'RAG and memory vectors' },
  { id: 'transcription', label: 'Transcription', detail: 'Audio to text' },
  { id: 'image', label: 'Image generation', detail: 'Image creation' },
  { id: 'tts', label: 'Text to speech', detail: 'Speech synthesis' },
  { id: 'reranker', label: 'Reranker', detail: 'RAG result ranking' },
];

function asObject(value: unknown): Record<string, any> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : {};
}

function cloneConfig(config: RuntimeConfig): RuntimeConfig {
  return JSON.parse(JSON.stringify(config || {})) as RuntimeConfig;
}

function prettyConfig(config: RuntimeConfig): string {
  return JSON.stringify(config, null, 2);
}

export function Settings({ fetchRuntime, pushActivity }: SettingsProps) {
  const [config, setConfig] = useState<RuntimeConfig>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [advancedText, setAdvancedText] = useState('');
  const errorRef = useRef<HTMLDivElement | null>(null);

  const models = useMemo(() => asObject(asObject(config.models).active), [config]);
  const endpoints = useMemo(() => asObject(asObject(config.models).endpoints), [config]);

  useEffect(() => {
    void loadConfig();
  }, []);

  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);

  async function loadConfig() {
    setLoading(true);
    setError('');
    try {
      const response = await fetchRuntime('/v1/config');
      if (!response.ok) throw new Error(`config ${response.status}`);
      const data = await response.json() as RuntimeConfig;
      setConfig(data);
      setAdvancedText(prettyConfig(data));
    } catch (loadError) {
      const message = String(loadError);
      setError(message);
      pushActivity('error', 'Load config failed', message);
    } finally {
      setLoading(false);
    }
  }

  function updateConfig(mutator: (next: RuntimeConfig) => void) {
    setConfig((previous) => {
      const next = cloneConfig(previous);
      mutator(next);
      setAdvancedText(prettyConfig(next));
      return next;
    });
  }

  function updateModel(kind: ModelKind, value: string) {
    updateConfig((next) => {
      next.models = asObject(next.models);
      next.models.active = asObject(next.models.active);
      next.models.active[kind] = value;
    });
  }

  function updateEndpoint(kind: ModelKind, field: string, value: string) {
    updateConfig((next) => {
      next.models = asObject(next.models);
      next.models.endpoints = asObject(next.models.endpoints);
      next.models.endpoints[kind] = asObject(next.models.endpoints[kind]);
      next.models.endpoints[kind][field] = value;
    });
  }

  function updateNested(section: string, field: string, value: string | number | boolean) {
    updateConfig((next) => {
      next[section] = asObject(next[section]);
      next[section][field] = value;
    });
  }

  async function saveConfig(event?: FormEvent) {
    event?.preventDefault();
    setError('');
    const chatModel = String(models.chat || '').trim();
    const chatProvider = String(asObject(endpoints.chat).provider || '').trim();
    if (!chatModel || !chatProvider) {
      setError('Chat model and provider are required before saving.');
      return;
    }
    setSaving(true);
    try {
      const response = await fetchRuntime('/v1/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
      });
      if (!response.ok) throw new Error(`save config ${response.status}`);
      const saved = await response.json() as RuntimeConfig;
      setConfig(saved);
      setAdvancedText(prettyConfig(saved));
      pushActivity('status', 'Config saved', 'Changes are active for subsequent requests. Sensitive values remain hidden.');
    } catch (saveError) {
      const message = String(saveError);
      setError(message);
      pushActivity('error', 'Save config failed', message);
    } finally {
      setSaving(false);
    }
  }

  function applyAdvanced() {
    try {
      const parsed = JSON.parse(advancedText) as RuntimeConfig;
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Configuration must be a JSON object.');
      setConfig(parsed);
      setError('');
    } catch (parseError) {
      setError(`Advanced JSON: ${String(parseError)}`);
    }
  }

  function exportConfig() {
    const blob = new Blob([prettyConfig(config)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'luckyagent-config-redacted.json';
    link.click();
    URL.revokeObjectURL(url);
    pushActivity('status', 'Config exported', 'Export contains no API keys, tokens, or authorization headers.');
  }

  function importConfig(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const parsed = JSON.parse(String(reader.result || '')) as RuntimeConfig;
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Import must contain a JSON object.');
        setConfig(parsed);
        setAdvancedText(prettyConfig(parsed));
        setError('');
      } catch (importError) {
        setError(`Import failed: ${String(importError)}`);
      }
    };
    reader.readAsText(file);
    event.target.value = '';
  }

  if (loading) {
    return (
      <section className="settings-panel" aria-busy="true">
        <div className="settings-loading">Loading configuration…</div>
      </section>
    );
  }

  const configuredCount = MODEL_KINDS.filter(({ id }) => String(models[id] || '').trim()).length;

  return (
    <form className="settings-panel" onSubmit={saveConfig}>
      <header className="page-head">
        <div className="page-head-text">
          <span className="eyebrow">Runtime</span>
          <h2>Configuration</h2>
          <p className="settings-desc">
            Changes apply to future requests. Existing API keys are never sent back to this browser.
          </p>
        </div>
        <div className="panel-actions">
          <button className="ghost" type="button" onClick={() => void loadConfig()}>Reload</button>
          <button className="ghost" type="button" onClick={exportConfig}>Export safe copy</button>
          <label className="ghost import-button">
            Import
            <input type="file" accept="application/json" onChange={importConfig} />
          </label>
          <button className="primary" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save and apply'}</button>
        </div>
      </header>

      <div ref={errorRef} className="settings-error" role="alert" tabIndex={-1} hidden={!error}>{error}</div>
      <p className="settings-live" role="status">{saving ? 'Saving configuration. Please wait.' : ''}</p>

      <section className="settings-section" aria-labelledby="models-heading">
        <div className="section-head">
          <div>
            <h3 id="models-heading">Models</h3>
            <p className="settings-desc">
              Each purpose can use its own provider, endpoint, protocol, and credential.
            </p>
          </div>
          <span className="section-count">{configuredCount} of {MODEL_KINDS.length} configured</span>
        </div>

        <label className="field" htmlFor="vision-mode">
          <span>Image understanding</span>
          <select id="vision-mode" value={String(asObject(config.models).vision_mode || 'auto')}
            onChange={(event) => updateNested('models', 'vision_mode', event.target.value)}>
            <option value="auto">Automatic — use the chat model when it supports images</option>
            <option value="external">Always use the separate vision model</option>
          </select>
        </label>
        <p className="settings-desc">Automatic mode sends each image directly to a vision-capable chat model. Otherwise, the separate vision model supplies a text description.</p>
        <div className="model-grid">
          {MODEL_KINDS.map(({ id, label, detail }) => {
            const endpoint = asObject(endpoints[id]);
            const modelID = String(models[id] || '');
            const provider = String(endpoint.provider || '');
            const base = String(endpoint.api_base || '');
            const protocol = String(endpoint.protocol || '');
            const isSet = Boolean(modelID.trim());
            return (
              <section className={`model-card ${isSet ? '' : 'unset'}`} key={id} aria-labelledby={`model-head-${id}`}>
                <div className="model-card-head">
                  <div>
                    <h4 id={`model-head-${id}`}>{label}</h4>
                    <p>{detail}</p>
                  </div>
                  <span className={`model-state ${isSet ? 'on' : 'off'}`}>{isSet ? 'Active' : 'Not set'}</span>
                </div>

                <label className="field" htmlFor={`model-${id}`}>
                  <span>Model ID</span>
                  <input
                    id={`model-${id}`}
                    name={`model-${id}`}
                    value={modelID}
                    onChange={(event) => updateModel(id, event.target.value)}
                    placeholder="Not configured"
                    spellCheck={false}
                  />
                </label>

                <details className="disclosure">
                  <summary>Connection settings</summary>
                  <div className="disclosure-body">
                    <label className="field" htmlFor={`provider-${id}`}>
                      <span>Provider</span>
                      <input id={`provider-${id}`} name={`provider-${id}`} value={provider} onChange={(event) => updateEndpoint(id, 'provider', event.target.value)} placeholder="openai" spellCheck={false} />
                    </label>
                    <label className="field" htmlFor={`base-${id}`}>
                      <span>API base</span>
                      <input id={`base-${id}`} name={`base-${id}`} value={base} onChange={(event) => updateEndpoint(id, 'api_base', event.target.value)} placeholder="https://api.example.com/v1" spellCheck={false} />
                    </label>
                    <label className="field" htmlFor={`key-${id}`}>
                      <span>New API key</span>
                      <input id={`key-${id}`} name={`key-${id}`} type="password" autoComplete="off" placeholder="Leave blank to keep the current key" onChange={(event) => updateEndpoint(id, 'api_key', event.target.value)} />
                    </label>
                    <label className="field" htmlFor={`protocol-${id}`}>
                      <span>Protocol</span>
                      <input id={`protocol-${id}`} name={`protocol-${id}`} value={protocol} onChange={(event) => updateEndpoint(id, 'protocol', event.target.value)} placeholder="chat_completions or responses" spellCheck={false} />
                    </label>
                  </div>
                </details>
              </section>
            );
          })}
        </div>
      </section>

      <section className="settings-section" aria-labelledby="agent-heading">
        <div className="section-head">
          <div>
            <h3 id="agent-heading">Agent and server</h3>
            <p className="settings-desc">Limits for a single turn, and the address the local API listens on.</p>
          </div>
        </div>
        <div className="settings-card settings-grid">
          <label className="field" htmlFor="agent-timeout">
            <span>Agent timeout</span>
            <input id="agent-timeout" type="number" min="1" value={Number(asObject(config.agent).timeout_seconds || 60)} onChange={(event) => updateNested('agent', 'timeout_seconds', Number(event.target.value))} />
            <small>Seconds before a turn is abandoned.</small>
          </label>
          <label className="field" htmlFor="agent-iterations">
            <span>Maximum iterations</span>
            <input id="agent-iterations" type="number" min="1" value={Number(asObject(config.agent).max_iterations || 10)} onChange={(event) => updateNested('agent', 'max_iterations', Number(event.target.value))} />
            <small>Tool-calling rounds allowed per turn.</small>
          </label>
          <label className="field" htmlFor="server-address">
            <span>Local server address</span>
            <input id="server-address" value={String(asObject(config.server).addr || '')} onChange={(event) => updateNested('server', 'addr', event.target.value)} spellCheck={false} />
            <small>Restart the runtime to apply.</small>
          </label>
        </div>
      </section>

      <section className="settings-section" aria-labelledby="advanced-heading">
        <div className="section-head">
          <div>
            <h3 id="advanced-heading">Advanced</h3>
            <p className="settings-desc">Gateways, RAG, memory, autonomy, and hooks are edited as raw JSON.</p>
          </div>
        </div>
        <details className="settings-card advanced-config">
          <summary>Edit configuration JSON</summary>
          <div className="disclosure-body">
            <label className="field" htmlFor="advanced-json">
              <span>Configuration JSON</span>
              <textarea id="advanced-json" value={advancedText} onChange={(event) => setAdvancedText(event.target.value)} spellCheck={false} rows={18} />
            </label>
            <button className="ghost" type="button" onClick={applyAdvanced}>Use JSON in form</button>
          </div>
        </details>
      </section>
    </form>
  );
}
