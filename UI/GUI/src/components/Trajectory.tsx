import { useEffect, useMemo, useState } from 'react';
import type { ActivityNote, SessionToolTrace, ToolTraceRecord } from '../types';

type TraceFilter = 'all' | 'success' | 'failure';

type TrajectoryProps = {
  session: string;
  fetchRuntime: (path: string, init?: RequestInit) => Promise<Response>;
  pushActivity: (kind: ActivityNote['kind'], title: string, body: string, meta?: string) => void;
  onOpenChat: () => void;
};

function formatDuration(duration?: number): string {
  if (!duration) return 'duration unavailable';
  if (duration < 1000) return `${duration} ms`;
  return `${(duration / 1000).toFixed(duration >= 10000 ? 0 : 1)} s`;
}

function formatPayload(value?: string): string {
  const text = value?.trim();
  if (!text) return 'No data recorded.';
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

function matchesQuery(record: ToolTraceRecord, query: string): boolean {
  if (!query) return true;
  const haystack = [record.name, record.annotation, record.arguments, record.result, record.error]
    .filter(Boolean)
    .join('\n')
    .toLocaleLowerCase();
  return haystack.includes(query.toLocaleLowerCase());
}

function rateLabel(value?: number): string {
  if (value === undefined || !Number.isFinite(value)) return '—';
  return `${Math.round(value * 100)}%`;
}

export function Trajectory({ session, fetchRuntime, pushActivity, onOpenChat }: TrajectoryProps) {
  const [trace, setTrace] = useState<SessionToolTrace | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState<TraceFilter>('all');
  const [query, setQuery] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function loadTrace() {
      const id = session.trim();
      if (!id) {
        setTrace(null);
        setError('Select a session to inspect its trajectory.');
        return;
      }

      setLoading(true);
      setError('');
      try {
        const response = await fetchRuntime(`/v1/sessions/${encodeURIComponent(id)}/tools`);
        if (!response.ok) throw new Error(`tool trace ${response.status}`);
        const payload = (await response.json()) as SessionToolTrace;
        if (!cancelled) setTrace(payload);
      } catch (loadError) {
        if (!cancelled) {
          const message = String(loadError);
          setTrace(null);
          setError(message);
          pushActivity('error', 'Trajectory unavailable', message);
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    void loadTrace();
    return () => {
      cancelled = true;
    };
  }, [session, refreshKey]);

  const records = trace?.tools || [];
  const filteredRecords = useMemo(() => records.filter((record) => {
    if (filter === 'success' && !record.success) return false;
    if (filter === 'failure' && record.success) return false;
    return matchesQuery(record, query.trim());
  }), [filter, query, records]);

  function exportTrace() {
    if (!trace) return;
    const data = JSON.stringify(trace, null, 2);
    const blob = new Blob([data], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `${trace.session_id || 'session'}-tool-trajectory.json`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  const visibleLabel = filteredRecords.length === records.length
    ? `${records.length} calls`
    : `${filteredRecords.length} of ${records.length} calls`;

  return (
    <div className="trajectory">
      <header className="trajectory-head">
        <div>
          <div className="eyebrow">Session observability</div>
          <h2>Tool trajectory</h2>
          <p>{session || 'No session selected'}</p>
        </div>
        <div className="trajectory-actions">
          <button className="ghost" type="button" onClick={onOpenChat}>
            Open chat
          </button>
          <button className="ghost" type="button" onClick={() => setRefreshKey((value) => value + 1)} disabled={loading}>
            {loading ? 'Refreshing' : 'Refresh'}
          </button>
          <button className="primary" type="button" onClick={exportTrace} disabled={!trace}>
            Export JSON
          </button>
        </div>
      </header>

      <section className="trajectory-summary" aria-label="Tool trajectory summary">
        <div>
          <span>Total calls</span>
          <strong>{trace?.total_calls ?? records.length}</strong>
        </div>
        <div>
          <span>Successful</span>
          <strong className="success-text">{trace?.successes ?? records.filter((record) => record.success).length}</strong>
        </div>
        <div>
          <span>Failed</span>
          <strong className="failure-text">{trace?.failures ?? records.filter((record) => !record.success).length}</strong>
        </div>
        <div>
          <span>Success rate</span>
          <strong>{rateLabel(trace?.success_rate)}</strong>
        </div>
      </section>

      <section className="trajectory-toolbar" aria-label="Trajectory filters">
        <label className="trajectory-search">
          <span>Search</span>
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Tool, annotation, input, or result"
            spellCheck={false}
          />
        </label>
        <div className="trace-filter-group" aria-label="Result filter">
          {([
            ['all', 'All'],
            ['success', 'Successful'],
            ['failure', 'Failed'],
          ] as const).map(([value, label]) => (
            <button
              className={`trace-filter ${filter === value ? 'active' : ''}`}
              type="button"
              key={value}
              aria-pressed={filter === value}
              onClick={() => setFilter(value)}
            >
              {label}
            </button>
          ))}
        </div>
      </section>

      {error ? <div className="trajectory-empty error-text">{error}</div> : null}
      {!error && !loading && records.length === 0 ? (
        <div className="trajectory-empty">
          <strong>No tool calls recorded</strong>
          <span>Tool activity will appear here after this session runs tools.</span>
        </div>
      ) : null}
      {!error && !loading && records.length > 0 && filteredRecords.length === 0 ? (
        <div className="trajectory-empty">No calls match the current filters.</div>
      ) : null}

      {filteredRecords.length > 0 ? (
        <section className="trajectory-list" aria-label={visibleLabel}>
          <div className="trajectory-list-head">
            <span>{visibleLabel}</span>
            <span>Most recent session record last</span>
          </div>
          {filteredRecords.map((record, index) => (
            <article className={`trajectory-event ${record.success ? 'success' : 'failure'}`} key={`${record.name}-${index}-${record.arguments || ''}`}>
              <div className="trajectory-marker" aria-hidden="true">
                <span>{index + 1}</span>
              </div>
              <div className="trajectory-event-body">
                <div className="trajectory-event-head">
                  <div>
                    <span className="trajectory-tool-name">{record.name || 'unnamed tool'}</span>
                    <span className={`trace-state ${record.success ? 'success' : 'failure'}`}>
                      {record.success ? 'Succeeded' : 'Failed'}
                    </span>
                  </div>
                  <small>{formatDuration(record.duration_ms)}</small>
                </div>
                <p>{record.annotation || `Called ${record.name || 'a tool'}.`}</p>
                {record.error ? <div className="trace-error">{record.error}</div> : null}
                <details className="trajectory-details">
                  <summary>Inspect input and result</summary>
                  <div className="trajectory-payloads">
                    <section>
                      <span>Input</span>
                      <pre>{formatPayload(record.arguments)}</pre>
                    </section>
                    <section>
                      <span>{record.success ? 'Result' : 'Failure detail'}</span>
                      <pre>{formatPayload(record.result || record.error)}</pre>
                    </section>
                  </div>
                </details>
              </div>
            </article>
          ))}
        </section>
      ) : null}
    </div>
  );
}
