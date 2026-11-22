FROM python:3.12.16-slim-bookworm
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    HOME=/tmp
WORKDIR /app/inference
COPY inference/requirements.txt ./requirements.txt
RUN pip install torch==2.6.0 --index-url https://download.pytorch.org/whl/cu124 \
 && pip install -r requirements.txt
COPY inference/ ./
RUN mkdir -p data weights && chmod 777 data weights
CMD ["sh", "-c", "exec python -m uvicorn main:app --host \"$HOST_BIND\" --port \"$PORT\""]
