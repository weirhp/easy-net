FROM node:20-slim

WORKDIR /app

# 1. 复制依赖描述并安装生产环境依赖
COPY package*.json ./
RUN npm install --omit=dev

# 2. 复制核心代码
COPY server.js ./
COPY public ./public

# 3. 配置默认端口 (Hugging Face 强制要求监听 7860，在 VPS 部署时可以通过 Compose 覆盖为其他端口)
ENV PORT=7860
ENV DATA_DIR=/app/data
EXPOSE 7860

# 4. 启动服务
CMD ["node", "server.js"]
