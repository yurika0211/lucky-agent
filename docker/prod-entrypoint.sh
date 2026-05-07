#!/bin/sh
set -eu

export HOME="${HOME:-/var/lib/luckyharness}"

mkdir -p "$HOME"

luckyharness init >/tmp/luckyharness-init.log 2>&1 || true

set_config() {
  key="$1"
  value="$2"
  if [ -n "$value" ]; then
    luckyharness config set "$key" "$value" >/tmp/luckyharness-config.log 2>&1
  fi
}

set_config "provider" "${LH_PROVIDER:-}"
set_config "api_key" "${LH_API_KEY:-}"
set_config "api_base" "${LH_API_BASE:-}"
set_config "model" "${LH_MODEL:-}"
set_config "max_tokens" "${LH_MAX_TOKENS:-}"
set_config "temperature" "${LH_TEMPERATURE:-}"
set_config "soul_path" "${LH_SOUL_PATH:-}"

set_config "server.addr" "${LH_API_ADDR:-}"
set_config "server.api_keys" "${LH_API_KEYS:-}"
set_config "server.rate_limit" "${LH_RATE_LIMIT:-}"
set_config "server.log_level" "${LH_LOG_LEVEL:-}"
set_config "server.log_format" "${LH_LOG_FORMAT:-}"
set_config "server.enable_cors" "${LH_ENABLE_CORS:-}"
set_config "server.cors_origins" "${LH_CORS_ORIGINS:-}"
set_config "server.metrics_addr" "${LH_METRICS_ADDR:-}"

set_config "web_search.provider" "${LH_WEB_SEARCH_PROVIDER:-}"
set_config "web_search.api_key" "${LH_WEB_SEARCH_API_KEY:-}"
set_config "web_search.base_url" "${LH_WEB_SEARCH_BASE_URL:-}"
set_config "web_search.max_results" "${LH_WEB_SEARCH_MAX_RESULTS:-}"
set_config "web_search.proxy" "${LH_WEB_SEARCH_PROXY:-}"

set_config "msg_gateway.platform" "${LH_MSG_GATEWAY_PLATFORM:-}"
set_config "msg_gateway.start_all" "${LH_MSG_GATEWAY_START_ALL:-}"
set_config "msg_gateway.telegram.token" "${LH_TELEGRAM_TOKEN:-}"
set_config "msg_gateway.telegram.proxy" "${LH_TELEGRAM_PROXY:-}"
set_config "msg_gateway.telegram.chat_timeout_seconds" "${LH_TELEGRAM_CHAT_TIMEOUT_SECONDS:-}"
set_config "msg_gateway.telegram.progress_as_messages" "${LH_TELEGRAM_PROGRESS_AS_MESSAGES:-}"
set_config "msg_gateway.telegram.progress_as_natural_language" "${LH_TELEGRAM_PROGRESS_AS_NATURAL_LANGUAGE:-}"
set_config "msg_gateway.telegram.progress_summary_with_llm" "${LH_TELEGRAM_PROGRESS_SUMMARY_WITH_LLM:-}"
set_config "msg_gateway.telegram.show_tool_details_in_result" "${LH_TELEGRAM_SHOW_TOOL_DETAILS_IN_RESULT:-}"

exec luckyharness "$@"
