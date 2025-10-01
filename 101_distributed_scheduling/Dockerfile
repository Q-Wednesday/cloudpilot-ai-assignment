FROM golang:1.25.1 AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main main.go

FROM alpine:3.19
WORKDIR /
COPY --from=builder /app/main /main
ENTRYPOINT ["/main"]
