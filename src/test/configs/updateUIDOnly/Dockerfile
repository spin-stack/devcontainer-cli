FROM debian:latest

ARG LOCAL_GID
RUN groupadd --gid $LOCAL_GID bar || true

RUN groupadd --gid 4321 foo
RUN useradd --uid 1234 --gid 4321 --create-home foo
