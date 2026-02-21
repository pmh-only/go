FROM scratch

ARG TARGETARCH

COPY go-${TARGETARCH} /go

VOLUME ["/data"]

ENV DB_FILE=/data/urls.db

EXPOSE 80

ENTRYPOINT ["/go"]
