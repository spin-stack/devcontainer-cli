



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
echo "_CONTAINER_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'root' || grep -E '^root|^[^:]*:[^:]*:root:' /etc/passwd || true) | cut -d: -f6)" >> /tmp/dev-container-features/devcontainer-features.builtin.env && \
echo "_REMOTE_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'root' || grep -E '^root|^[^:]*:[^:]*:root:' /etc/passwd || true) | cut -d: -f6)" >> /tmp/dev-container-features/devcontainer-features.builtin.env

ENV SDKMAN_DIR="/usr/local/sdkman"
ENV PATH="${SDKMAN_DIR}/bin:${PATH}"
RUN --mount=type=bind,from=dev_containers_feature_content_source,source=sdk_0,target=/tmp/build-features-src/sdk_0 \
    cp -ar /tmp/build-features-src/sdk_0 /tmp/dev-container-features \
 && chmod -R 0755 /tmp/dev-container-features/sdk_0 \
 && cd /tmp/dev-container-features/sdk_0 \
 && chmod +x ./devcontainer-features-install.sh \
 && ./devcontainer-features-install.sh \
 && rm -rf /tmp/dev-container-features/sdk_0





ARG _DEV_CONTAINERS_IMAGE_USER=root
USER $_DEV_CONTAINERS_IMAGE_USER



ENV 2="two"
ENV 10="ten"
ENV ZED="1"
ENV ALPHA="\${ZED}"
