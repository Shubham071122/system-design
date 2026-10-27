FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

FROM alpine:3.20

WORKDIR /app
RUN apk --no-cache add curl
COPY --from=builder /app/server /app/server

EXPOSE 8080
CMD ["/app/server"]
