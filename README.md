# scan-wifi

Firmware ESP32 + servidor Go + frontend 3D + microsserviço PyTorch/CUDA para presença e postura via Wi-Fi CSI.

## Stack

| Item | Tecnologia |
|------|------------|
| firmware/ | ESP-IDF (C), CSI via Wi-Fi/UDP |
| server/ | Go (UDP multi-ESP, ping, bridge IA) + UI Three.js em `web/` |
| inference/ | FastAPI + PyTorch CNN-1D (CUDA / Quadro T1000) |
| scripts/ | `run_all.sh`, `flash_esp32.sh` |

## Quick start

```bash
cp .env.example .env
make inference-venv
./scripts/run_all.sh
```

Abra `http://127.0.0.1:8787`. CSI chega por UDP (`CSI_UDP=:9001`). Sem ESPs: `make server-demo` + `make inference-run`.

Flash dos ESPs (um de cada vez no USB):

```bash
make flash-esp32-node1 SERIAL=/dev/ttyUSB0
make flash-esp32-node2 SERIAL=/dev/ttyUSB0
```

## Scripts

| Comando | Função |
|---------|--------|
| `./scripts/run_all.sh` | Sobe inference + server juntos |
| `./scripts/flash_esp32.sh` | Build + flash do firmware |
| `make server-build` | Binário Go em `server/scan-wifi` |
| `make server-run` / `server-demo` | Go UDP multi-ESP / demo |
| `make inference-run` | Só o microsserviço Python |
| `make inference-health` | `GET /health` (CUDA) |
| `make firmware-build|flash|monitor|run` | ESP-IDF |

## Env

Arquivos: `.env` (local), `.env.prod` (produção), `.env.example` (git).

| Chave | Default | Descrição |
|-------|---------|-----------|
| `HOST_BIND` | `127.0.0.1` | Bind HTTP |
| `PORT` / `HOST_PORT` | `8787` | Porta do server Go |
| `INFERENCE_SERVER_URL` | `http://127.0.0.1:8790` | URL do cérebro |
| `INFERENCE_PORT` | `8790` | Porta do FastAPI |
| `DEVICE` | `cuda` | `cuda` ou `cpu` |
| `SERIAL` | `/dev/ttyUSB0` | só flash do firmware |
| `CSI_UDP` | `:9001` | escuta CSI dos ESPs |
| `CSI_UDP_HOST` | IP LAN do PC | destino UDP no flash do firmware |
| `CSI_NODE_ID` | `ESP_NODE_01` | id do nó no flash (`01` / `02`) |
| `ROUTER` | `192.168.1.1` | gateway para ping CSI |
| `CSI_STIM` | (vazio) | IPs extras de estímulo (CSV) |
| `ROOM_W` / `ROOM_D` | `6` / `4.5` | Sala em metros |
| `CALIBRATE_PACKETS` | `50` | Amostras por postura |

## API

| Serviço | Prefixo | Auth |
|---------|---------|------|
| Go (BFF/UI) | `/`, `/ws`, `/api/calibrate` | local |
| Inference (S2S) | `/predict`, `/calibrate`, `/health` | local |

## Estrutura

```
firmware/     Olho — ESP32 CSI
server/       Maestro — Go + web/ (UI embutida)
inference/    Cérebro — FastAPI/PyTorch
scripts/      Automações
specs/        Planos de feature
```


## Treino CSI (GPU)

Com inference + server rodando:

```bash
# 1) coletar (ESP/demo ativo)
./scripts/collect_pose.sh standing
./scripts/collect_pose.sh sitting
./scripts/collect_pose.sh lying
# repetir algumas vezes cada postura

# 2) treinar na Quadro T1000
make train

# 3) o serviço recarrega weights/model_weights.pth
curl -X POST http://127.0.0.1:8790/weights/reload
```

Endpoints: `POST /collect/start`, `POST /collect/save`, `GET /collect/status`, `POST /predict`.
