# 管理后台（构建为静态文件，nginx 提供并反代 /api 到控制面）
FROM node:22-alpine AS build
# npm 源：国内加速；可用 --build-arg NPM_REGISTRY=https://registry.npmjs.org 覆盖回官方
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm config set registry "${NPM_REGISTRY}" && npm install --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
