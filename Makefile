.PHONY: build install test clean init run serve chat tg qq napcat weixin dashboard tui ui-install ui-build ui-dev ui-typecheck

HOME_DIR := $(CURDIR)/.lh-home

ifeq ($(OS),Windows_NT)
LH_BIN := .\lh.exe
ifeq ($(wildcard lh.exe),lh.exe)
LH_CMD := $(LH_BIN)
else
LH_CMD := go run ./cmd/lh
endif
RUN_WITH_HOME = set "HOME=$(HOME_DIR)" &&
REMOVE_LH = if exist lh del /q lh & if exist lh.exe del /q lh.exe
else
LH_CMD := go run ./cmd/lh
RUN_WITH_HOME = HOME="$(HOME_DIR)"
REMOVE_LH = rm -f lh lh.exe
endif

build:
	go build -o lh ./cmd/lh

install: build
	mkdir -p "$$HOME/.luckyharness/runtime"
	printf '%s\n' "$(CURDIR)/UI" > "$$HOME/.luckyharness/runtime/tui-ui-dir"
	go install ./cmd/lh

test:
	go test ./...

init:
	$(RUN_WITH_HOME) $(LH_CMD) init

run: serve

serve:
	$(RUN_WITH_HOME) $(LH_CMD) serve

chat:
	$(RUN_WITH_HOME) $(LH_CMD) chat

tg:
	$(RUN_WITH_HOME) $(LH_CMD) msg-gateway start --platform telegram

qq:
	$(RUN_WITH_HOME) $(LH_CMD) msg-gateway start --platform qqofficial

napcat:
	$(RUN_WITH_HOME) $(LH_CMD) msg-gateway start --platform napcat

weixin:
	$(RUN_WITH_HOME) $(LH_CMD) msg-gateway start --platform weixin

dashboard:
	$(RUN_WITH_HOME) $(LH_CMD) dashboard start

tui:
	$(RUN_WITH_HOME) $(LH_CMD) tui

ui-install:
	cd UI && npm install

ui-build:
	cd UI && npm run build

ui-dev:
	cd UI && npm run dev --workspace GUI

ui-typecheck:
	cd UI && npm run typecheck

clean:
	$(REMOVE_LH)
