FROM python:3.13-alpine

RUN addgroup -S importer && adduser -S -G importer importer

COPY --chown=importer:importer import_tasks.py /usr/local/bin/import_tasks.py
COPY --chown=importer:importer easy_tasks.py /usr/local/bin/easy_tasks.py
COPY --chown=importer:importer seed.py /usr/local/bin/seed.py

USER importer

ENTRYPOINT ["python", "/usr/local/bin/import_tasks.py"]
