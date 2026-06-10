# 示例业务服务（暴露 /openapi.json 与业务接口）
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/demo .

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/demo /app/demo
EXPOSE 9000
ENTRYPOINT ["/app/demo"]
