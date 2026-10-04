FROM node:22-slim
WORKDIR /app

COPY package*.json ./
RUN npm install

COPY tsconfig.json ./
COPY src ./src
RUN npm run build

# The built prompts. Without this line the agent has no instructions.
COPY sops/build ./sops/build

CMD ["node", "dist/main.js", "start"]
