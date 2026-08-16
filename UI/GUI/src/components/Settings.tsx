import { useState, useEffect } from 'react';
import type { ActivityNote } from '../types';

interface SettingsProps {
  fetchRuntime: (path: string, init?: RequestInit) => Promise<Response>;
  pushActivity: (kind: ActivityNote['kind'], title: string, body: string, meta?: string) => void;
}

export function Settings({ fetchRuntime, pushActivity }: SettingsProps) {
  const [config, setConfig] = useState<Record<string, any>>({});
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    loadConfig();
  }, []);

  async function loadConfig() {
    setLoading(true);
    try {
      const response = await fetchRuntime('/v1/config');
      if (!response.ok) throw new Error(`config ${response.status}`);
      const data = await response.json();
      setConfig(data);
    } catch (error) {
      pushActivity('error', 'Load config failed', String(error));
    } finally {
      setLoading(false);
    }
  }

  async function saveConfig() {
    setSaving(true);
    try {
      const response = await fetchRuntime('/v1/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config),
      });
      if (!response.ok) throw new Error(`save config ${response.status}`);
      pushActivity('status', 'Config saved', 'Configuration updated successfully');
    } catch (error) {
      pushActivity('error', 'Save config failed', String(error));
    } finally {
      setSaving(false);
    }
  }

  function updateField(key: string, value: any) {
    setConfig((prev) => ({ ...prev, [key]: value }));
  }

  if (loading) {
    return (
      <div className="settings-panel">
        <div className="panel-head">
          <h2>Settings</h2>
        </div>
        <div className="panel-body">Loading configuration...</div>
      </div>
    );
  }

  return (
    <div className="settings-panel">
      <div className="panel-head">
        <h2>Settings</h2>
        <div className="panel-actions">
          <button className="ghost" type="button" onClick={loadConfig}>
            Reload
          </button>
          <button className="primary" type="button" onClick={saveConfig} disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>

      <div className="settings-body">
        <div className="settings-section">
          <h3>Runtime Configuration</h3>
          <p className="settings-desc">
            Changes here will be synced directly to config.json
          </p>

          {Object.keys(config).length === 0 ? (
            <div className="empty-line">No configuration available</div>
          ) : (
            <div className="settings-grid">
              {Object.entries(config).map(([key, value]) => (
                <div className="setting-item" key={key}>
                  <label>
                    <span>{key}</span>
                    {typeof value === 'boolean' ? (
                      <input
                        type="checkbox"
                        checked={value}
                        onChange={(e) => updateField(key, e.target.checked)}
                      />
                    ) : typeof value === 'number' ? (
                      <input
                        type="number"
                        value={value}
                        onChange={(e) => updateField(key, Number(e.target.value))}
                      />
                    ) : (
                      <input
                        type="text"
                        value={String(value)}
                        onChange={(e) => updateField(key, e.target.value)}
                      />
                    )}
                  </label>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
