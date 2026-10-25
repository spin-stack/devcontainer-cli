

FROM dev_container_feature_content_temp as dev_containers_feature_content_source

ARG _DEV_CONTAINERS_BASE_IMAGE=scratch

FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_feature_content_normalize
USER root
COPY --from=dev_containers_feature_content_source /tmp/build-features/devcontainer-features.builtin.env /tmp/build-features/
RUN chmod -R 0755 /tmp/build-features/

FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage

USER root

RUN mkdir -p /tmp/dev-container-features
COPY --from=dev_containers_feature_content_normalize /tmp/build-features/ /tmp/dev-container-features

RUN \
echo "_CONTAINER_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'root' || grep -E '^root|^[^:]*:[^:]*:root:' /etc/passwd || true) | cut -d: -f6)" >> /tmp/dev-container-features/devcontainer-features.builtin.env && \
echo "_REMOTE_USER_HOME=$( (command -v getent >/dev/null 2>&1 && getent passwd 'root' || grep -E '^root|^[^:]*:[^:]*:root:' /etc/passwd || true) | cut -d: -f6)" >> /tmp/dev-container-features/devcontainer-features.builtin.env


COPY --chown=root:root --from=dev_containers_feature_content_source /tmp/build-features/go_0 /tmp/dev-container-features/go_0
RUN chmod -R 0755 /tmp/dev-container-features/go_0 \
&& cd /tmp/dev-container-features/go_0 \
&& chmod +x ./devcontainer-features-install.sh \
&& ./devcontainer-features-install.sh





ARG _DEV_CONTAINERS_IMAGE_USER=root
USER $_DEV_CONTAINERS_IMAGE_USER




