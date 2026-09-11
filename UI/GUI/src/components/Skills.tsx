import { useEffect, useRef, useState } from 'react';
import type {
  ActivityNote,
  SkillInstall,
  SkillInstallsResponse,
  SkillScanFinding,
  SkillSummary,
  SkillsResponse,
} from '../types';

type PushActivity = (kind: ActivityNote['kind'], title: string, body: string, meta?: string) => void;

type SkillsProps = {
  fetchRuntime: (path: string, init?: RequestInit) => Promise<Response>;
  pushActivity: PushActivity;
};

/**
 * Poll interval for an in-flight install. The runtime rate-limits to 60 requests
 * per minute per client address, shared with the chat endpoint — polling at 1 Hz
 * would exhaust the budget and start 429ing chat from the same origin.
 */
const POLL_INTERVAL_MS = 2000;

/** Install states that will not change again without user action. */
const TERMINAL_STATUSES = new Set(['ready', 'rejected', 'failed', 'aborted', 'confirmed']);

/** Reads the server's error message out of a JSON body, whatever shape it took. */
async function errorFrom(response: Response, fallback: string): Promise<string> {
  const body = (await response.json().catch(() => ({}))) as Record<string, unknown>;
  return String(body.details || body.error || body.message || `${fallback} ${response.status}`);
}

function stateClass(state: string): string {
  if (state === 'enabled') return 'ok';
  if (state === 'error') return 'err';
  return 'idle';
}

function severityClass(severity: string): string {
  if (severity === 'block') return 'err';
  if (severity === 'warn') return 'warn';
  return 'info';
}

export function Skills({ fetchRuntime, pushActivity }: SkillsProps) {
  const [skills, setSkills] = useState<SkillSummary[]>([]);
  const [skillsDir, setSkillsDir] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  /** Name of the skill whose uninstall is awaiting a second click. */
  const [confirmRemove, setConfirmRemove] = useState('');

  const [install, setInstall] = useState<SkillInstall | null>(null);
  const [installError, setInstallError] = useState('');
  const [localPath, setLocalPath] = useState('');
  const [acceptWarnings, setAcceptWarnings] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const pollTimer = useRef<number | null>(null);

  async function loadSkills() {
    setLoading(true);
    setError('');
    try {
      const response = await fetchRuntime('/v1/skills');
      if (!response.ok) throw new Error(await errorFrom(response, 'skills'));
      const payload = (await response.json()) as SkillsResponse;
      setSkills(payload.skills || []);
      setSkillsDir(payload.skills_dir || '');
    } catch (err) {
      setError(String(err));
      pushActivity('error', 'Skills unavailable', String(err));
    } finally {
      setLoading(false);
    }
  }

  /** Per-skill lifecycle actions. All are POST; the server owns the state machine. */
  async function act(name: string, action: 'enable' | 'disable' | 'reload') {
    setBusy(`${name}:${action}`);
    try {
      const response = await fetchRuntime(`/v1/skills/${encodeURIComponent(name)}/${action}`, { method: 'POST' });
      if (!response.ok) throw new Error(await errorFrom(response, action));
      await loadSkills();
    } catch (err) {
      pushActivity('error', `${action} ${name} failed`, String(err));
      setError(String(err));
    } finally {
      setBusy('');
    }
  }

  async function uninstall(name: string) {
    setBusy(`${name}:uninstall`);
    try {
      const response = await fetchRuntime(`/v1/skills/${encodeURIComponent(name)}/uninstall`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        // Keep a snapshot so the removal stays reversible.
        body: JSON.stringify({ purge: false }),
      });
      if (!response.ok) throw new Error(await errorFrom(response, 'uninstall'));
      pushActivity('socket', `${name} uninstalled`, 'A snapshot was kept for rollback.');
      await loadSkills();
    } catch (err) {
      pushActivity('error', `Uninstall ${name} failed`, String(err));
      setError(String(err));
    } finally {
      setBusy('');
      setConfirmRemove('');
    }
  }

  async function rollback(name: string) {
    setBusy(`${name}:rollback`);
    try {
      const listed = await fetchRuntime(`/v1/skills/${encodeURIComponent(name)}/versions`);
      if (!listed.ok) throw new Error(await errorFrom(listed, 'versions'));
      const payload = (await listed.json()) as { versions?: string[] };
      const latest = (payload.versions || [])[0];
      if (!latest) throw new Error('no snapshots available');

      const response = await fetchRuntime(`/v1/skills/${encodeURIComponent(name)}/rollback`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version_id: latest }),
      });
      if (!response.ok) throw new Error(await errorFrom(response, 'rollback'));
      pushActivity('socket', `${name} rolled back`, `Restored snapshot ${latest}.`);
      await loadSkills();
    } catch (err) {
      pushActivity('error', `Rollback ${name} failed`, String(err));
      setError(String(err));
    } finally {
      setBusy('');
    }
  }

  async function reloadAll() {
    setBusy('reload-all');
    try {
      const response = await fetchRuntime('/v1/skills/reload', { method: 'POST' });
      if (!response.ok) throw new Error(await errorFrom(response, 'reload'));
      await loadSkills();
    } catch (err) {
      pushActivity('error', 'Reload failed', String(err));
      setError(String(err));
    } finally {
      setBusy('');
    }
  }

  /* --------------------------------------------------------------- install */

  function stopPolling() {
    if (pollTimer.current !== null) {
      window.clearTimeout(pollTimer.current);
      pollTimer.current = null;
    }
  }

  /** Polls one install until it reaches a terminal state. */
  function pollInstall(id: string) {
    stopPolling();
    const tick = async () => {
      try {
        const response = await fetchRuntime(`/v1/skills/installs/${encodeURIComponent(id)}`);
        if (!response.ok) throw new Error(await errorFrom(response, 'install'));
        const record = (await response.json()) as SkillInstall;
        setInstall(record);
        if (!TERMINAL_STATUSES.has(record.status)) {
          pollTimer.current = window.setTimeout(() => void tick(), POLL_INTERVAL_MS);
        }
      } catch (err) {
        setInstallError(String(err));
      }
    };
    pollTimer.current = window.setTimeout(() => void tick(), POLL_INTERVAL_MS);
    void tick();
  }

  async function startInstall(init: RequestInit) {
    setInstallError('');
    setAcceptWarnings(false);
    setBusy('install');
    try {
      const response = await fetchRuntime('/v1/skills/install', init);
      if (!response.ok) throw new Error(await errorFrom(response, 'install'));
      const record = (await response.json()) as SkillInstall;
      setInstall(record);
      pollInstall(record.install_id);
    } catch (err) {
      setInstallError(String(err));
      pushActivity('error', 'Install failed to start', String(err));
    } finally {
      setBusy('');
    }
  }

  function onArchiveSelected(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    // Reset so re-selecting the same file fires a change event again.
    event.target.value = '';
    if (!file) return;
    const form = new FormData();
    form.append('file', file);
    void startInstall({ method: 'POST', body: form });
  }

  function onInstallFromPath() {
    const path = localPath.trim();
    if (!path) return;
    void startInstall({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source: 'path', path }),
    });
  }

  async function confirmInstall() {
    if (!install) return;
    setBusy('confirm');
    try {
      const response = await fetchRuntime(`/v1/skills/installs/${encodeURIComponent(install.install_id)}/confirm`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ accept_warnings: acceptWarnings }),
      });
      if (!response.ok) throw new Error(await errorFrom(response, 'confirm'));
      pushActivity(
        'socket',
        `${install.name || 'Skill'} installed`,
        'Installed in the disabled state — enable it to make its tools callable.',
      );
      setInstall(null);
      await loadSkills();
    } catch (err) {
      setInstallError(String(err));
      pushActivity('error', 'Confirm failed', String(err));
    } finally {
      setBusy('');
    }
  }

  async function abortInstall() {
    if (!install) return;
    stopPolling();
    try {
      await fetchRuntime(`/v1/skills/installs/${encodeURIComponent(install.install_id)}/abort`, { method: 'POST' });
    } catch {
      // Aborting is best-effort; staging is garbage-collected on a TTL anyway.
    }
    setInstall(null);
    setInstallError('');
  }

  useEffect(() => {
    void loadSkills();
    // This panel unmounts on every tab switch, so an in-flight install is
    // recovered from the server rather than lifted into App state.
    void (async () => {
      try {
        const response = await fetchRuntime('/v1/skills/installs');
        if (!response.ok) return;
        const payload = (await response.json()) as SkillInstallsResponse;
        const pending = (payload.installs || []).find(
          (record) => record.status !== 'aborted' && record.status !== 'confirmed',
        );
        if (pending) {
          setInstall(pending);
          if (!TERMINAL_STATUSES.has(pending.status)) pollInstall(pending.install_id);
        }
      } catch {
        // No pending install, or the endpoint is unavailable. Nothing to restore.
      }
    })();
    return stopPolling;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const findings: SkillScanFinding[] = install?.scan?.findings || [];
  const blocking = findings.filter((f) => f.severity === 'block');
  const warnings = findings.filter((f) => f.severity === 'warn');
  const needsAcknowledgement = warnings.length > 0 && !acceptWarnings;
  const canConfirm = Boolean(install?.can_confirm) && !needsAcknowledgement;
  const inProgress = Boolean(install && !TERMINAL_STATUSES.has(install.status));

  return (
    <div className="skills">
      <header className="page-head">
        <div className="page-head-text">
          <span className="eyebrow">Runtime</span>
          <h2>Skills</h2>
          <p className="settings-desc">
            {skillsDir ? <>Loaded from <code>{skillsDir}</code>. </> : null}
            Installed skills land disabled — review their tools, then enable.
          </p>
        </div>
        <div className="panel-actions">
          <button className="ghost" type="button" onClick={() => void loadSkills()} disabled={loading}>
            {loading ? 'Refreshing' : 'Refresh'}
          </button>
          <button className="ghost" type="button" onClick={() => void reloadAll()} disabled={busy === 'reload-all'}>
            {busy === 'reload-all' ? 'Reloading' : 'Reload all'}
          </button>
        </div>
      </header>

      {error ? <div className="empty-line error-text">{error}</div> : null}

      {/* ------------------------------------------------------------ install */}
      <section className="panel skill-install">
        <div className="panel-head">
          <h2>Install a skill</h2>
          {install ? <span className={`status-chip ${install.status === 'ready' ? 'ok' : install.status === 'rejected' ? 'err' : 'idle'}`}>{install.status}</span> : null}
        </div>

        {!install ? (
          <div className="skill-install-sources">
            <label className="ghost import-button">
              Upload archive (.zip / .tar.gz)
              <input ref={fileInputRef} type="file" accept=".zip,.tar.gz,.tgz,.gz" onChange={onArchiveSelected} hidden />
            </label>
            <div className="field skill-path-field">
              <span>Or a directory on the server</span>
              <input
                type="text"
                value={localPath}
                placeholder="/srv/skills/my-skill"
                onChange={(event) => setLocalPath(event.target.value)}
              />
              <small>
                Disabled unless the path is under <code>skills.install.allowed_local_roots</code>.
              </small>
            </div>
            <button className="ghost" type="button" onClick={onInstallFromPath} disabled={!localPath.trim() || busy === 'install'}>
              Install from path
            </button>
          </div>
        ) : (
          <div className="skill-install-review">
            {inProgress ? (
              <div className="empty-line" aria-busy="true">
                {install.status === 'scanning'
                  ? 'Scanning the package — no skill code has run yet.'
                  : install.status === 'probing'
                    ? 'Inspecting the skill in a sandbox…'
                    : 'Staging the upload…'}
              </div>
            ) : null}

            {install.error ? <div className="empty-line error-text">{install.error}</div> : null}
            {installError ? <div className="empty-line error-text">{installError}</div> : null}

            {install.capability ? (
              <div className="skill-capability">
                <div className="kv-list">
                  <div className="kv">
                    <span>Name</span>
                    <strong>{install.capability.name}</strong>
                  </div>
                  {install.capability.description ? (
                    <div className="kv">
                      <span>Description</span>
                      <strong>{install.capability.description}</strong>
                    </div>
                  ) : null}
                  {install.capability.scripts?.length ? (
                    <div className="kv">
                      <span>Scripts</span>
                      <strong>{install.capability.scripts.join(', ')}</strong>
                    </div>
                  ) : null}
                  <div className="kv">
                    <span>Tools</span>
                    <strong>{install.capability.tools?.length ?? 0}</strong>
                  </div>
                </div>

                {install.capability.tools?.length ? (
                  <ul className="skill-tool-list">
                    {install.capability.tools.map((tool) => (
                      <li key={tool.name}>
                        <code>{tool.name}</code>
                        {tool.origin === 'cli_probe' ? <span className="muted-tag">from --help</span> : null}
                        <span className="muted"> {tool.description}</span>
                      </li>
                    ))}
                  </ul>
                ) : null}

                {install.capability.probe_errors?.length ? (
                  <div className="empty-line">
                    Sandbox probe incomplete: {install.capability.probe_errors.join('; ')}
                  </div>
                ) : null}
              </div>
            ) : null}

            {blocking.length > 0 ? (
              <div className="skill-findings">
                <h3 className="failure-text">Blocking ({blocking.length})</h3>
                <ul>
                  {blocking.map((finding, index) => (
                    <li key={`${finding.rule}-${index}`} className="skill-finding err">
                      <code>{finding.rule}</code>
                      <span>{finding.detail}</span>
                      {finding.path ? <span className="muted">{finding.path}{finding.line ? `:${finding.line}` : ''}</span> : null}
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}

            {warnings.length > 0 ? (
              <div className="skill-findings">
                <h3>Warnings ({warnings.length})</h3>
                <ul>
                  {warnings.map((finding, index) => (
                    <li key={`${finding.rule}-${index}`} className={`skill-finding ${severityClass(finding.severity)}`}>
                      <code>{finding.rule}</code>
                      <span>{finding.detail}</span>
                      {finding.path ? <span className="muted">{finding.path}{finding.line ? `:${finding.line}` : ''}</span> : null}
                    </li>
                  ))}
                </ul>
                <label className="skill-ack">
                  <input
                    type="checkbox"
                    checked={acceptWarnings}
                    onChange={(event) => setAcceptWarnings(event.target.checked)}
                  />
                  <span>I reviewed these warnings</span>
                </label>
              </div>
            ) : null}

            <div className="panel-actions">
              <button className="primary" type="button" onClick={() => void confirmInstall()} disabled={!canConfirm || busy === 'confirm'}>
                {busy === 'confirm' ? 'Installing…' : 'Confirm install'}
              </button>
              <button className="ghost" type="button" onClick={() => void abortInstall()}>
                {inProgress ? 'Cancel' : 'Discard'}
              </button>
            </div>
            {!install.can_confirm && !inProgress ? (
              <div className="empty-line error-text">
                {blocking.length > 0
                  ? 'This package cannot be installed: the findings above are not overridable.'
                  : 'This install is not in a confirmable state.'}
              </div>
            ) : null}
          </div>
        )}
      </section>

      {/* ----------------------------------------------------------- inventory */}
      <section className="panel">
        <div className="panel-head">
          <h2>Installed</h2>
          <span className="muted-tag">{skills.length} loaded</span>
        </div>

        {!error && skills.length === 0 ? (
          <div className="empty-line">No skills loaded. Install one above, or drop a directory into the skills folder.</div>
        ) : null}

        <div className="skill-list">
          {skills.map((skill) => {
            const removing = confirmRemove === skill.name;
            return (
              <article className="skill-item" key={skill.name}>
                <div className="skill-item-main">
                  <span className={`status-dot ${stateClass(skill.state)}`} />
                  <strong>{skill.name}</strong>
                  <span className={`status-chip ${stateClass(skill.state)}`}>{skill.state}</span>
                  {skill.managed ? <span className="muted-tag">managed</span> : null}
                  <span className="muted-tag">{skill.tool_count ?? 0} tools</span>
                  {skill.unhealthy_tools?.length ? (
                    <span className="status-chip err">{skill.unhealthy_tools.length} unhealthy</span>
                  ) : null}
                </div>

                {skill.description ? <p className="skill-item-desc">{skill.description}</p> : null}
                {skill.error ? <p className="empty-line error-text">{skill.error}</p> : null}

                <details className="disclosure skill-details">
                  <summary>Inspect tools and location</summary>
                  <div className="disclosure-body">
                    <div className="kv-list">
                      <div className="kv">
                        <span>Directory</span>
                        <strong><code>{skill.dir}</code></strong>
                      </div>
                      {skill.aliases?.length ? (
                        <div className="kv">
                          <span>Aliases</span>
                          <strong>{skill.aliases.join(', ')}</strong>
                        </div>
                      ) : null}
                      {skill.digest ? (
                        <div className="kv">
                          <span>Digest</span>
                          <strong><code>{skill.digest.slice(0, 23)}…</code></strong>
                        </div>
                      ) : null}
                      {skill.installed_at ? (
                        <div className="kv">
                          <span>Installed</span>
                          <strong>{new Date(skill.installed_at).toLocaleString()}</strong>
                        </div>
                      ) : null}
                    </div>
                    <ul className="skill-tool-list">
                      {(skill.tools || []).map((tool) => (
                        <li key={tool.full_name}>
                          <code>{tool.full_name}</code>
                          <span className={`status-chip ${tool.enabled ? 'ok' : 'idle'}`}>
                            {tool.registered ? (tool.enabled ? 'callable' : 'disabled') : 'unregistered'}
                          </span>
                          <span className="muted"> {tool.description}</span>
                        </li>
                      ))}
                    </ul>
                  </div>
                </details>

                <div className="skill-item-actions">
                  {skill.state === 'enabled' ? (
                    <button className="mini-button" type="button" onClick={() => void act(skill.name, 'disable')} disabled={busy.startsWith(skill.name)}>
                      Disable
                    </button>
                  ) : (
                    <button className="mini-button" type="button" onClick={() => void act(skill.name, 'enable')} disabled={busy.startsWith(skill.name)}>
                      Enable
                    </button>
                  )}
                  <button className="mini-button" type="button" onClick={() => void act(skill.name, 'reload')} disabled={busy.startsWith(skill.name)}>
                    Reload
                  </button>
                  {/* Rollback needs a snapshot, which only managed installs have. */}
                  {skill.managed && (skill.versions ?? 0) > 0 ? (
                    <button className="mini-button" type="button" onClick={() => void rollback(skill.name)} disabled={busy.startsWith(skill.name)}>
                      Roll back
                    </button>
                  ) : null}
                  {/* Two-step confirm: there is no dialog primitive in this UI. */}
                  <button
                    className={removing ? 'mini-button danger' : 'mini-button'}
                    type="button"
                    onClick={() => (removing ? void uninstall(skill.name) : setConfirmRemove(skill.name))}
                    onBlur={() => removing && setConfirmRemove('')}
                    disabled={busy.startsWith(skill.name)}
                  >
                    {removing ? 'Confirm uninstall?' : 'Uninstall'}
                  </button>
                </div>
              </article>
            );
          })}
        </div>
      </section>
    </div>
  );
}
