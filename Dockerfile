FROM golang:1.12 as builder
WORKDIR /go/src/github.com/camptocamp/tirsias
COPY . .
RUN make tirsias

FROM scratch
COPY --from=builder /go/src/github.com/camptocamp/tirsias/tirsias /
ENTRYPOINT ["/tirsias"]
CMD [""]
