# Build rosetta-sia binary
FROM golang:1.14 as builder
RUN git clone --depth 1 --branch v0.1.0 https://github.com/NebulousLabs/rosetta-sia \
  && cd rosetta-sia \
  && go build -o /app/rosetta-sia

## Build Final Image
FROM ubuntu:18.04
RUN mkdir -p /app \
  && chown -R nobody:nogroup /app \
  && mkdir -p /data \
  && chown -R nobody:nogroup /data
COPY --from=builder /app/rosetta-sia /app/rosetta-sia
EXPOSE 8080/tcp
EXPOSE 9381/tcp
CMD ["/app/rosetta-sia", "-d", "/data"]
