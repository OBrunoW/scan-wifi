# Inference — CSI Pose ML (PyTorch + CUDA + FastAPI)

Microsserviço local para postura (`standing` / `sitting` / `lying`), perfil volumétrico (`adult` / `child` / `pet`), coordenadas métricas e estabilização anti-drift (dead-zone 3 cm).

## GPU (Dell / Quadro T1000)

```bash
cd inference
python3.12 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
# se o torch do pip vier CPU-only:
pip install torch --index-url https://download.pytorch.org/whl/cu124
python -c "import torch; print(torch.cuda.is_available(), torch.cuda.get_device_name(0) if torch.cuda.is_available() else 'cpu')"
```

## API

```bash
# pela raiz do monorepo
make inference-run
# ou
DEVICE=cuda PORT=8790 .venv/bin/python -m uvicorn main:app --host 127.0.0.1 --port 8790
```

| Método | Path | Função |
|--------|------|--------|
| GET | `/health` | device + pesos + mapa espacial |
| POST | `/predict` | inferência (CNN + mapa + EMA) |
| POST | `/collect/start` | inicia coleta (`posture`, opcional `x,y,z`) |
| POST | `/collect/save` | grava `.npy` + meta JSON |
| POST | `/map/calibrate` | âncora espacial (`x,z` + CSI) |
| GET | `/map/status` | âncoras salvas |
| POST | `/map/reset` | limpa mapa + estabilizador |
| POST | `/calibrate` | perfil postural (legado) |
| POST | `/weights/reload` | recarrega `csi_model_weights.pth` |

## Coleta → treino

1. Com ESP/Go enviando CSI, colete cada postura (idealmente com XYZ real):

```bash
curl -X POST localhost:8790/collect/start -H 'content-type: application/json' \
  -d '{"posture":"standing","x":0,"y":0.9,"z":1.5}'
# … aguarde ~2s de CSI via /collect/push (ou script make collect-*) …
curl -X POST localhost:8790/collect/save -H 'content-type: application/json' -d '{}'
```

2. Treine na GPU:

```bash
make train
# salva weights/csi_model_weights.pth (+ cópia model_weights.pth)
```

3. Calibre o mapa da sala (mín. 2 âncoras; ideal: cantos + centro):

```bash
curl -X POST localhost:8790/map/calibrate -H 'content-type: application/json' -d '{
  "label":"center","x":0,"z":0,"y":0,
  "packets":[ /* janela CSI */ ]
}'
```

## Estabilização

Em `/predict`: EMA nas coordenadas, dead-zone ~4 cm (trava tremor) e histerese de postura (4 frames). Deitado força `Y ≈ 0.2 m`.

## Módulos

- `model.py` — Conv1d multi-task
- `dataset.py` / `collector.py` — `.npy` rotulados
- `mapping.py` — IDW de âncoras CSI→(X,Z)
- `stabilize.py` — EMA + dead-zone + histerese
- `train.py` — Adam, CE + MSE, CUDA
- `main.py` — FastAPI


## Cenario 3 — 2x ESP32 (UDP)

1. Flash dois ESPs com `CSI_NODE_ID=ESP_NODE_01` e `ESP_NODE_02`, mesmo Wi-Fi, `CSI_UDP_HOST` = IP da workstation.
2. Server: `make server-run` (escuta UDP `:9001` por padrao).
3. `/predict` recebe `packets` + `packets2` e triangula com `esp_x/z` e `esp2_x/z`.
