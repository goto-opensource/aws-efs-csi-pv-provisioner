FROM golang:1.14 AS build

WORKDIR /app

ADD ./ /app/
RUN go build -ldflags '-extldflags "-fno-PIC -static"' -buildmode pie -tags 'osusergo netgo static_build' . && strip efs-csi-pv-provisioner
RUN go test .

FROM scratch

ENTRYPOINT ["/efs-csi-pv-provisioner"]

COPY --from=build /app/efs-csi-pv-provisioner /
