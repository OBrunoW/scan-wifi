ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
ifneq (,$(wildcard $(ROOT)/.env))
include $(ROOT)/.env
export
endif

SERIAL ?= /dev/ttyUSB0
ROUTER ?= 192.168.1.1
HOST_BIND ?= 127.0.0.1
PORT   ?= 8787
ROOM_W ?= 8
ROOM_D ?= 12
CSI_UDP ?= :9001
CSI_STIM ?=
CSI_UDP_HOST ?= 192.168.1.26
CSI_NODE_ID ?= ESP_NODE_01
INFERENCE_URL ?= http://127.0.0.1:8790
INFERENCE_PORT ?= 8790
IDF_EXPORT ?= ~/.espressif/v6.1/esp-idf/export.sh
VENV := $(ROOT)/inference/.venv

.PHONY: help \
	firmware-build firmware-flash firmware-monitor firmware-run \
	server-build server-run server-demo \
	inference-venv inference-run inference-health \
	collect-standing collect-sitting collect-lying collect-save collect-status \
	train make-train map-status map-reset \
	run-all flash-esp32 flash-esp32-node1 flash-esp32-node2 flash-both up \
	build-firmware build-server run-server

help:
	@echo "Firmware:  make firmware-build|flash|monitor|run"
	@echo "Server:    make server-build|server-run|server-demo"
	@echo "Inference: make inference-venv|inference-run|inference-health"
	@echo "ML:        make collect-standing|sitting|lying + collect-save POSTURE=..."
	@echo "Train:     make train   (alias: make-train)"
	@echo "All:       make run-all   |   ./scripts/run_all.sh"
	@echo "Flash:     make flash-both  (grava os dois ESPs; SERIAL1 e SERIAL2 nas portas)"
	@echo "           make flash-esp32-node1 | flash-esp32-node2 SERIAL=/dev/ttyUSB0"
	@echo "Subir:     make up  (inference + server)"

firmware-build:
	. $(IDF_EXPORT) && cd $(ROOT)/firmware && idf.py build

firmware-flash:
	sg dialout -c '. $(IDF_EXPORT) && cd $(ROOT)/firmware && idf.py -p $(SERIAL) flash'

firmware-monitor:
	sg dialout -c '. $(IDF_EXPORT) && cd $(ROOT)/firmware && idf.py -p $(SERIAL) monitor'

firmware-run: firmware-build
	sg dialout -c '. $(IDF_EXPORT) && cd $(ROOT)/firmware && idf.py -p $(SERIAL) flash monitor'

flash-esp32:
	SERIAL=$(SERIAL) IDF_EXPORT=$(IDF_EXPORT) CSI_NODE_ID=$(CSI_NODE_ID) CSI_UDP_HOST=$(CSI_UDP_HOST) $(ROOT)/scripts/flash_esp32.sh

flash-esp32-node1:
	$(MAKE) flash-esp32 CSI_NODE_ID=ESP_NODE_01 CSI_UDP_HOST=$(CSI_UDP_HOST) SERIAL=$(SERIAL)

flash-esp32-node2:
	$(MAKE) flash-esp32 CSI_NODE_ID=ESP_NODE_02 CSI_UDP_HOST=$(CSI_UDP_HOST) SERIAL=$(SERIAL)

SERIAL1 ?= /dev/ttyUSB0
SERIAL2 ?= /dev/ttyUSB1

flash-both:
	$(MAKE) flash-esp32-node1 SERIAL=$(SERIAL1)
	$(MAKE) flash-esp32-node2 SERIAL=$(SERIAL2)

up: run-all

server-build:
	cd $(ROOT)/server && go build -o scan-wifi ./cmd/server

server-run:
	cd $(ROOT)/server && HOST_BIND=$(HOST_BIND) PORT=$(PORT) INFERENCE_SERVER_URL=$(INFERENCE_URL) CSI_STIM="$(CSI_STIM)" go run ./cmd/server -router $(ROUTER) -port $(PORT) -room-w $(ROOM_W) -room-d $(ROOM_D) -udp $(CSI_UDP) -stim "$(CSI_STIM)" -inference $(INFERENCE_URL)

server-demo:
	cd $(ROOT)/server && HOST_BIND=$(HOST_BIND) PORT=$(PORT) INFERENCE_SERVER_URL=$(INFERENCE_URL) CSI_STIM="$(CSI_STIM)" go run ./cmd/server -no-ui -port $(PORT) -room-w $(ROOM_W) -room-d $(ROOM_D) -udp $(CSI_UDP) -stim "$(CSI_STIM)" -inference $(INFERENCE_URL)

inference-venv:
	rm -rf $(VENV)
	python3.12 -m venv $(VENV)
	$(VENV)/bin/pip install --upgrade pip
	$(VENV)/bin/pip install torch --index-url https://download.pytorch.org/whl/cu124
	$(VENV)/bin/pip install -r $(ROOT)/inference/requirements.txt

inference-run:
	cd $(ROOT)/inference && 	HOST_BIND=$(HOST_BIND) PORT=$(INFERENCE_PORT) DEVICE=cuda 	$(VENV)/bin/python -m uvicorn main:app --host $(HOST_BIND) --port $(INFERENCE_PORT)

inference-health:
	curl -sS $(INFERENCE_URL)/health | python3 -m json.tool

run-all:
	$(ROOT)/scripts/run_all.sh

build-firmware: firmware-build
build-server: server-build
run-server: server-run


collect-standing:
	PORT=$(PORT) INFERENCE_SERVER_URL=$(INFERENCE_URL) $(ROOT)/scripts/collect_pose.sh standing

collect-sitting:
	PORT=$(PORT) INFERENCE_SERVER_URL=$(INFERENCE_URL) $(ROOT)/scripts/collect_pose.sh sitting

collect-lying:
	PORT=$(PORT) INFERENCE_SERVER_URL=$(INFERENCE_URL) $(ROOT)/scripts/collect_pose.sh lying

POSTURE ?= standing
collect-save:
	PORT=$(PORT) INFERENCE_SERVER_URL=$(INFERENCE_URL) $(ROOT)/scripts/collect_pose.sh $(POSTURE)

collect-status:
	curl -sS $(INFERENCE_URL)/collect/status | python3 -m json.tool

map-status:
	curl -sS $(INFERENCE_URL)/map/status | python3 -m json.tool

map-reset:
	curl -sS -X POST $(INFERENCE_URL)/map/reset | python3 -m json.tool

train:
	cd $(ROOT)/inference && DEVICE=cuda $(VENV)/bin/python train.py --epochs 40 --batch 16

make-train: train

compose-build:
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) docker compose build

compose-up:
	HOST_UID=$$(id -u) HOST_GID=$$(id -g) docker compose up -d --build
