FROM python:3.13-alpine

RUN addgroup -g 1001 -S importer && adduser -u 1001 -S -G importer importer && \
    mkdir -p /bots /bot-control && chown 1001:1001 /bots /bot-control && chmod 0700 /bots /bot-control

COPY --chown=importer:importer import_tasks.py /usr/local/bin/import_tasks.py
COPY --chown=importer:importer easy_tasks.py /usr/local/bin/easy_tasks.py
COPY --chown=importer:importer seed.py /usr/local/bin/seed.py
COPY --chown=importer:importer bot_manifest.py /usr/local/bin/bot_manifest.py

USER importer

ENTRYPOINT ["python", "/usr/local/bin/import_tasks.py"]
