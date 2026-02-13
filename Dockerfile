FROM ubuntu:16.04 as builder

ENV DEBIAN_FRONTEND=noninteractive \
    TZ=UTC

RUN apt-get update && \
    apt-get install --yes --no-install-recommends \
    curl openssl zip tar ca-certificates build-essential git \
    && apt-get clean \
    && rm --recursive --force /var/lib/apt/lists/* /tmp/* /var/tmp/*

RUN sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/bin

RUN curl -L https://go.dev/dl/go1.26.0.linux-amd64.tar.gz -O && \
    tar -xf go1.26.0.linux-amd64.tar.gz

ENV PATH="${PATH}:/go/bin:/root/go/bin"

WORKDIR /src

COPY . .

RUN task build


FROM scratch

WORKDIR /freeglm

COPY --from=builder /src/bin/freeglm freeglm

CMD ["/freeglm/freeglm","-listen","0.0.0.0:5000"]
