



ARG _DEV_CONTAINERS_BASE_IMAGE=scratch

FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_feature_content_normalize
USER root
COPY --from=dev_containers_feature_content_source devcontainer-features.builtin.env /tmp/build-features/
RUN chmod -R 0755 /tmp/build-features/

FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage

USER root

RUN mkdir -p /tmp/dev-container-features
COPY --from=dev_containers_feature_content_normalize /tmp/build-features/ /tmp/dev-container-features

RUN \
echo "_CONTAINER_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'vscode' || grep -E '^vscode|^[^:]*:[^:]*:vscode:' /etc/passwd || true) | cut -d: -f6)" >> /tmp/dev-container-features/devcontainer-features.builtin.env && \
echo "_REMOTE_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'dev\'o' || grep -E '^dev\'o|^[^:]*:[^:]*:dev\'o:' /etc/passwd || true) | cut -d: -f6)" >> /tmp/dev-container-features/devcontainer-features.builtin.env

ENV PATH="/usr/local/node/bin:${PATH}"
ENV Q="a\"b\\c"
RUN --mount=type=bind,from=dev_containers_feature_content_source,source=node_0,target=/tmp/build-features-src/node_0 \
    cp -ar /tmp/build-features-src/node_0 /tmp/dev-container-features \
 && chmod -R 0755 /tmp/dev-container-features/node_0 \
 && cd /tmp/dev-container-features/node_0 \
 && chmod +x ./devcontainer-features-install.sh \
 && ./devcontainer-features-install.sh \
 && rm -rf /tmp/dev-container-features/node_0


RUN --mount=type=bind,from=dev_containers_feature_content_source,source=hello_1,target=/tmp/build-features-src/hello_1 \
    cp -ar /tmp/build-features-src/hello_1 /tmp/dev-container-features \
 && chmod -R 0755 /tmp/dev-container-features/hello_1 \
 && cd /tmp/dev-container-features/hello_1 \
 && chmod +x ./devcontainer-features-install.sh \
 && ./devcontainer-features-install.sh \
 && rm -rf /tmp/dev-container-features/hello_1





ARG _DEV_CONTAINERS_IMAGE_USER=root
USER $_DEV_CONTAINERS_IMAGE_USER

LABEL devcontainer.metadata="[ \
{\"id\":\"ghcr.io/o/f/node:1\"}, \
{\"remoteEnv\":{\"A\":\"\$x\\\"y\\\\z\"}} \
]"

ENV A="1"
ENV B="x\$y\"z"
