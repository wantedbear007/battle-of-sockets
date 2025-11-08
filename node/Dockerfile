
FROM node:20 AS builder

WORKDIR /app

COPY package*.json ./
COPY tsconfig.json ./
RUN npm install

COPY . .

RUN npm run build

CMD ["node", "dist/src/app.js"]
