{ pkgs ? import <nixpkgs> { } }:

# Optional local environment. CI and application images use their own toolchains.
# FHS libraries allow the Chromium downloaded by Playwright to run unchanged.
(pkgs.buildFHSEnv {
  name = "task-per-minute";
  targetPkgs = p: with p; [
    bashInteractive
    coreutils findutils gnugrep gnused gawk which
    git gnumake gcc go nodejs_24
    (python3.withPackages (ps: [ ps.pyyaml ]))
    docker-client docker-compose ffmpeg curl cacert jq yq-go
    glib nss nspr atk at-spi2-atk at-spi2-core cups dbus
    libdrm libgbm libxkbcommon mesa alsa-lib expat
    libx11 libxcb libxcomposite libxdamage libxext libxfixes libxrandr
    pango cairo udev fontconfig freetype dejavu_fonts
  ];
  profile = ''
    # Use Playwright's project-matched downloads, not a system browser bundle.
    export PLAYWRIGHT_BROWSERS_PATH="''${XDG_CACHE_HOME:-$HOME/.cache}/ms-playwright"
    unset PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS
  '';
  runScript = "bash";
}).env
