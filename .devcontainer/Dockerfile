ARG VARIANT=1.24-bookworm

# FROM mcr.microsoft.com/devcontainers/go:1-${VARIANT}
FROM mcr.microsoft.com/devcontainers/base:debian-12

# [Optional] Uncomment this line to install global node packages.
# RUN su vscode -c "source /usr/local/share/nvm/nvm.sh && npm install -g <your-package-here>" 2>&1

RUN apt-get update && DEBIAN_FRONTEND=nointeractive apt-get install -y --no-install-recommends \
    locales tzdata lv zsh vim git curl jq ca-certificates
RUN echo "ja_JP.UTF-8 UTF-8" >> /etc/locale.gen
RUN locale-gen
RUN ln -sf /usr/share/zoneinfo/Asia/Tokyo /etc/localtime
RUN echo "Asia/Tokyo" > /etc/timezone

RUN chsh -s /usr/bin/zsh vscode
USER vscode
