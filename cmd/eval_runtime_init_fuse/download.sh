#!/bin/bash
# Init container: mount S3 bucket/prefix with s3fs-fuse and copy data to /test_data.
# Credentials are read from the mounted secret at /var/run/secrets/test-data.
set -euo pipefail

SECRET_DIR="/var/run/secrets/test-data"
DEST_DIR="/test_data"
MOUNT_DIR="/mnt/s3"

read_secret() {
    local file="${SECRET_DIR}/${1}"
    if [[ -f "${file}" ]]; then
        cat "${file}" | tr -d '[:space:]'
    fi
}

BUCKET="${TEST_DATA_S3_BUCKET:-}"
KEY_PREFIX="${TEST_DATA_S3_KEY:-}"
ACCESS_KEY="$(read_secret AWS_ACCESS_KEY_ID)"
SECRET_KEY="$(read_secret AWS_SECRET_ACCESS_KEY)"
REGION="$(read_secret AWS_DEFAULT_REGION)"
ENDPOINT="$(read_secret AWS_S3_ENDPOINT)"

if [[ -z "${BUCKET}" || -z "${KEY_PREFIX}" ]]; then
    echo "ERROR: TEST_DATA_S3_BUCKET and TEST_DATA_S3_KEY are required" >&2
    exit 1
fi
if [[ -z "${ACCESS_KEY}" || -z "${SECRET_KEY}" || -z "${REGION}" ]]; then
    echo "ERROR: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_DEFAULT_REGION secrets are required" >&2
    exit 1
fi

# Strip leading slash from key prefix
KEY_PREFIX="${KEY_PREFIX#/}"

echo "Starting s3fs-fuse download: s3://${BUCKET}/${KEY_PREFIX}"

mkdir -p "${MOUNT_DIR}" "${DEST_DIR}"

# Write s3fs password file
PASSWD_FILE="$(mktemp)"
echo "${ACCESS_KEY}:${SECRET_KEY}" > "${PASSWD_FILE}"
chmod 600 "${PASSWD_FILE}"

S3FS_OPTS="passwd_file=${PASSWD_FILE},use_path_request_style,allow_other,uid=0,gid=0,mp_umask=022,no_check_certificate"
if [[ -n "${ENDPOINT}" ]]; then
    S3FS_OPTS="${S3FS_OPTS},url=${ENDPOINT},use_path_request_style"
fi
if [[ -n "${REGION}" ]]; then
    S3FS_OPTS="${S3FS_OPTS},endpoint=${REGION}"
fi

s3fs "${BUCKET}:/${KEY_PREFIX}" "${MOUNT_DIR}" -o "${S3FS_OPTS}"

echo "s3fs mounted at ${MOUNT_DIR}, copying to ${DEST_DIR}..."
START_TS=$(date +%s%3N)
cp -r "${MOUNT_DIR}/." "${DEST_DIR}/"
END_TS=$(date +%s%3N)
ELAPSED=$(( END_TS - START_TS ))

fusermount -u "${MOUNT_DIR}" || true
rm -f "${PASSWD_FILE}"

echo "Copy complete in ${ELAPSED}ms"
