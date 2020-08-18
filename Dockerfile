# Build rosetta-sia binary
FROM golang:1.14 as builder
COPY . rosetta-sia/
RUN cd rosetta-sia && go build -o /app/rosetta-sia
# TODO: once repo is public, change to:
# RUN git clone https://github.com/NebulousLabs/rosetta-sia \
#   && cd rosetta-sia \
#   && go build -o /app/sia

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
