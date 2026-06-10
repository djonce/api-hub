# 示例业务服务（暴露 /openapi.json 与业务接口）
FROM golang:1.22-alpine AS build
# Go 模块代理：国内加速，direct 兜底保证海外仍可用；可用 --build-arg GOPROXY=... 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/demo .

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/demo /app/demo
EXPOSE 9000
ENTRYPOINT ["/app/demo"]
