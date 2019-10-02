FROM golang:1.12 as builder
RUN apt-get update && apt-get install ca-certificates -y
WORKDIR /go/src/github.com/camptocamp/tirsias
COPY . .
RUN make tirsias

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /go/src/github.com/camptocamp/tirsias/tirsias /
ENTRYPOINT ["/tirsias"]
CMD [""]
