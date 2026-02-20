FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o go .

# ----

FROM scratch

COPY --from=builder /app/go /go

VOLUME ["/data"]

ENV DB_FILE=/data/urls.db

EXPOSE 80

ENTRYPOINT ["/go"]
