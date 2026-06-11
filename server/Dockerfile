# 控制面（注册中心 + 控制台 API）
FROM golang:1.22-alpine AS build
# Go 模块代理：国内加速，direct 兜底保证海外仍可用；可用 --build-arg GOPROXY=... 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
# Alpine apk 源：留空走官方 dl-cdn；可用 --build-arg APK_MIRROR=mirrors.aliyun.com 国内加速
ARG APK_MIRROR=
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s|dl-cdn.alpinelinux.org|$APK_MIRROR|g" /etc/apk/repositories; fi && apk add --no-cache git
WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

FROM alpine:3.19
ARG APK_MIRROR=
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s|dl-cdn.alpinelinux.org|$APK_MIRROR|g" /etc/apk/repositories; fi && apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/server /app/server
EXPOSE 8080
ENTRYPOINT ["/app/server"]
