FROM node:20-alpine

WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev
COPY worker.js ./

ENV NATS_URL=nats://nats:4222
CMD ["npm", "start"]
