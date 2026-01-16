FROM python:3.12-slim

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY worker.py ./

ENV NATS_URL=nats://nats:4222
CMD ["python", "worker.py"]
