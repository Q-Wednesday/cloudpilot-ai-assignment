#!/bin/bash
set -euo pipefail

NAMESPACE=${1:-webhook-demo}
SERVICE=${2:-webhook-svc}
SECRET=${3:-webhook-certs}

TMPDIR=$(mktemp -d)
echo "Generating certs in ${TMPDIR} ..."

# 生成 CA
openssl genrsa -out ${TMPDIR}/ca.key 2048
openssl req -x509 -new -nodes -key ${TMPDIR}/ca.key -subj "/CN=Admission Controller Webhook CA" -days 3650 -out ${TMPDIR}/ca.crt

# 生成服务端 key
openssl genrsa -out ${TMPDIR}/tls.key 2048

# CSR 配置
cat > ${TMPDIR}/csr.conf <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${SERVICE}
DNS.2 = ${SERVICE}.${NAMESPACE}
DNS.3 = ${SERVICE}.${NAMESPACE}.svc
EOF

# 生成 CSR
openssl req -new -key ${TMPDIR}/tls.key -subj "/CN=${SERVICE}.${NAMESPACE}.svc" -out ${TMPDIR}/server.csr -config ${TMPDIR}/csr.conf

# 使用 CA 签发证书
openssl x509 -req -in ${TMPDIR}/server.csr -CA ${TMPDIR}/ca.crt -CAkey ${TMPDIR}/ca.key -CAcreateserial \
  -out ${TMPDIR}/tls.crt -days 3650 -extensions v3_req -extfile ${TMPDIR}/csr.conf

# 创建 Secret
kubectl -n ${NAMESPACE} delete secret ${SECRET} --ignore-not-found
kubectl -n ${NAMESPACE} create secret generic ${SECRET} \
  --from-file=tls.key=${TMPDIR}/tls.key \
  --from-file=tls.crt=${TMPDIR}/tls.crt \
  --from-file=ca.crt=${TMPDIR}/ca.crt

echo "✅ Done."
echo "CA cert saved to ${TMPDIR}/ca.crt"
echo "Now patch your MutatingWebhookConfiguration with this CA:"
echo
cat ${TMPDIR}/ca.crt | base64 | tr -d '\n'
echo
