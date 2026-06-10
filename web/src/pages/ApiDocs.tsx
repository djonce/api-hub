import { useEffect, useRef, useState } from "react";
import { Card, Alert, Spin } from "antd";
import { Link, useParams } from "react-router-dom";
import { openapiUrl } from "../lib/api";

const SWAGGER_VERSION = "5.17.14";
const CSS = `https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/${SWAGGER_VERSION}/swagger-ui.min.css`;
const JS = `https://cdnjs.cloudflare.com/ajax/libs/swagger-ui/${SWAGGER_VERSION}/swagger-ui-bundle.min.js`;

function loadCss(href: string) {
  if (document.querySelector(`link[href="${href}"]`)) return;
  const l = document.createElement("link");
  l.rel = "stylesheet";
  l.href = href;
  document.head.appendChild(l);
}

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    if (document.querySelector(`script[src="${src}"]`)) return resolve();
    const s = document.createElement("script");
    s.src = src;
    s.onload = () => resolve();
    s.onerror = () => reject(new Error("加载 Swagger UI 失败（需要外网访问 CDN）"));
    document.body.appendChild(s);
  });
}

export default function ApiDocs() {
  const { id } = useParams();
  const sid = Number(id);
  const ref = useRef<HTMLDivElement>(null);
  const [err, setErr] = useState<string>("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    loadCss(CSS);
    loadScript(JS)
      .then(() => {
        if (cancelled || !ref.current) return;
        const SwaggerUIBundle = (window as unknown as { SwaggerUIBundle?: any }).SwaggerUIBundle;
        if (!SwaggerUIBundle) {
          setErr("Swagger UI 未就绪");
          return;
        }
        SwaggerUIBundle({
          url: openapiUrl(sid),
          domNode: ref.current,
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis],
        });
        setLoading(false);
      })
      .catch((e) => setErr(String(e)));
    return () => {
      cancelled = true;
    };
  }, [sid]);

  return (
    <Card title="接口文档（Swagger UI）" extra={<Link to={`/services/${sid}`}>返回服务详情</Link>}>
      {err && <Alert type="error" message={err} style={{ marginBottom: 16 }} />}
      {loading && !err && <Spin tip="加载文档中..." style={{ marginBottom: 16 }} />}
      <div ref={ref} />
    </Card>
  );
}
