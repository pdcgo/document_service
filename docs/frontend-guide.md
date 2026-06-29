# DocumentService — Frontend Guide

How to upload and serve files (product images, avatars, warehouse photos, invoices, …) from the
frontend using **`DocumentService`** (`document_iface.v1`).

Files are uploaded **directly from the browser to Google Cloud Storage** via a short-lived **signed
URL** — the bytes never pass through our backend. You then reference the stored file by its
`documentId`. Some resource types get a **stable public URL** (great for `<img src>`); the rest are
served via short-lived **signed download URLs**.

```
 ┌──────────┐   1. RequestUpload    ┌─────────────┐
 │ frontend │ ────────────────────▶ │ DocumentSvc │   (returns a signed PUT url + upload_token)
 │          │ ◀──────────────────── │             │
 │          │                       └─────────────┘
 │          │   2. PUT file bytes        ┌─────┐
 │          │ ─────────────────────────▶ │ GCS │   (direct, cross-origin, uses the signed url)
 │          │                            └─────┘
 │          │   3. ConfirmUpload     ┌─────────────┐
 │          │ ────────────────────▶ │ DocumentSvc │   (returns document_id + public_url)
 └──────────┘                        └─────────────┘
        4. later: GetDownloadUrl(document_id)  →  url  (public stable url, or signed & expiring)
```

---

## 1. Prerequisites

- The generated TS client exists at `src/gen/document_iface/v1/*_pb.ts` and a `documentClient` is
  exported from `src/lib/clients.ts`:
  ```ts
  // src/lib/clients.ts
  import { createClient } from "@connectrpc/connect";
  import { DocumentService } from "../gen/document_iface/v1/document_pb";
  import { transport } from "./transport";

  export const documentClient = createClient(DocumentService, transport);
  ```
- **Auth is automatic.** The shared `transport` (`src/lib/transport.ts`) already attaches
  `Authorization: Bearer <jwt>` to every call — you never set headers on these RPCs yourself.
- The signed-in user **must have a role in the `teamId`** you upload for (`RequestUpload` is
  team-scoped). Get the active team from the team context:
  ```ts
  const { currentTeam } = useTeam();         // src/team/TeamContext.tsx
  const teamId = currentTeam!.teamId;        // bigint
  ```
- **Endpoint:** `DocumentService` must be served by whatever `transport.baseUrl` points at. The dev
  omnibus on `http://localhost:8086` does **not** currently include `document_service` — point the
  transport at an environment that serves it (the production omnibus, or the standalone service on
  `:8087`), or have it added to the dev omnibus.

> Types note: 64-bit proto fields are **`bigint`** in TS. `teamId` and `sizeBytes` are `bigint`
> (e.g. `70n`), and `resourceType` is the generated `DocumentResourceType` enum.

---

## 2. The contract

### `RequestUpload` — get a signed upload URL  *(team-scoped: token + role in `teamId`)*

| Request field  | Type                   | Notes |
|----------------|------------------------|-------|
| `teamId`       | `bigint`               | Must be a team you have a role in. |
| `resourceType` | `DocumentResourceType` | See the table below. Required (not `UNSPECIFIED`). |
| `contentType`  | `string`               | MIME type, e.g. `"image/jpeg"`. Must be allowed for the resource type. |
| `sizeBytes`    | `bigint`               | File size; must be `> 0` and `≤ 10 MiB`. |
| `filename`     | `string`               | Original name (stored as metadata; optional). |

| Response field | Type                  | Notes |
|----------------|-----------------------|-------|
| `uploadUrl`    | `string`              | Signed GCS URL — **PUT the bytes here**. |
| `method`       | `string`              | Always `"PUT"`. |
| `headers`      | `Record<string,string>` | Headers you **must** echo on the PUT (currently `{ "Content-Type": <contentType> }`). |
| `uploadToken`  | `string`              | Opaque HMAC token — pass to `ConfirmUpload`. |
| `expiresAt`    | `Timestamp`           | URL expiry (~15 minutes). PUT before this. |

### `ConfirmUpload` — finalize the upload  *(authenticated)*

| Request field | Type     | | Response field | Type     | Notes |
|---------------|----------|-|----------------|----------|-------|
| `uploadToken` | `string` | | `documentId`   | `string` | Reference this id from your business entity. |
|               |          | | `publicUrl`    | `string` | **Stable, permanent URL** — non-empty only for public resource types; empty for private. |

### `GetDownloadUrl` — get a viewable URL  *(authenticated)*

| Request field | Type     | | Response field | Type        | Notes |
|---------------|----------|-|----------------|-------------|-------|
| `documentId`  | `string` | | `url`          | `string`    | Public stable URL, or a short-lived signed URL. |
|               |          | | `public`       | `boolean`   | `true` → `url` is stable (no expiry); `false` → signed & expiring. |
|               |          | | `expiresAt`    | `Timestamp` | Set only when `public === false`. |

### Resource types & visibility

| `DocumentResourceType` | Value | Visibility | Allowed content types |
|------------------------|-------|------------|-----------------------|
| `PRODUCT`              | 1     | **public** | `image/jpeg`, `image/jpg`, `image/png`, `image/webp` |
| `PROFILE_PICTURE`      | 3     | **public** | images (as above) |
| `WAREHOUSE`            | 4     | **public** | images (as above) |
| `TRANSACTION`          | 2     | private    | `application/pdf` + images |
| `INVOICE`              | 5     | private    | `application/pdf` + images |
| `GENERAL`              | 6     | private    | `application/pdf` + images |
| `UNSPECIFIED`          | 0     | —          | invalid — will be rejected |

- **public** → `ConfirmUpload` returns a `publicUrl` you can store and render directly forever.
- **private** → no `publicUrl`; call `GetDownloadUrl` each time you need to show it (short-lived).
- **Max file size: 10 MiB** for all types.

---

## 3. Upload flow (plain TypeScript)

Framework-agnostic helpers built on `documentClient`. Drop them in a util module and call from any UI.

```ts
import { documentClient } from "../lib/clients";
import { DocumentResourceType } from "../gen/document_iface/v1/document_pb";

// 1) Ask the backend for a signed upload URL.
async function requestUpload(input: {
  teamId: bigint;
  resourceType: DocumentResourceType;
  file: File;
}) {
  return documentClient.requestUpload({
    teamId: input.teamId,
    resourceType: input.resourceType,
    contentType: input.file.type,            // e.g. "image/jpeg"
    sizeBytes: BigInt(input.file.size),
    filename: input.file.name,
  });
}

// 2) PUT the raw bytes straight to GCS.
//    CRITICAL: echo `headers` verbatim, send the raw File as the body (NOT FormData),
//    and never modify/re-encode `uploadUrl`.
async function putFile(uploadUrl: string, file: File, headers: Record<string, string>) {
  const res = await fetch(uploadUrl, {
    method: "PUT",
    headers,          // { "Content-Type": "<the exact contentType you requested>" }
    body: file,       // raw bytes — do not wrap in FormData / Blob parts
  });
  if (!res.ok) {
    throw new Error(`upload failed: ${res.status} ${await res.text()}`);
  }
}

// 3) Confirm — promotes the object and returns the document id (+ public url for public types).
async function confirmUpload(uploadToken: string) {
  return documentClient.confirmUpload({ uploadToken });
}

// Orchestrator: returns { documentId, publicUrl } (publicUrl is "" for private types).
export async function uploadDocument(input: {
  teamId: bigint;
  resourceType: DocumentResourceType;
  file: File;
}) {
  const req = await requestUpload(input);
  await putFile(req.uploadUrl, input.file, req.headers);
  const done = await confirmUpload(req.uploadToken);
  return { documentId: done.documentId, publicUrl: done.publicUrl };
}
```

---

## 4. Displaying & downloading

**Public types** (product / profile picture / warehouse) — persist `publicUrl` on your entity and
render it directly:

```ts
const { documentId, publicUrl } = await uploadDocument({
  teamId,
  resourceType: DocumentResourceType.PROFILE_PICTURE,
  file,
});
// store documentId on the user/product; render publicUrl:  <img src={publicUrl} />
```

**Private types** (invoice / transaction / general) — there is no public URL. Fetch a fresh signed
URL when you actually need to show/download it, and **don't persist** it (it expires in ~15 min):

```ts
async function viewDocument(documentId: string): Promise<string> {
  const res = await documentClient.getDownloadUrl({ documentId });
  return res.url; // res.public === false here; res.expiresAt ~15 min out — refetch when it expires
}
```

> Tip: `GetDownloadUrl` also works for public documents — it just returns the stable `publicUrl`
> with `public === true` and no `expiresAt`. If you didn't store `publicUrl`, you can always
> recover it this way from the `documentId`.

---

## 5. Client-side validation & errors

Validate before calling `RequestUpload` to fail fast (the backend enforces the same rules):

```ts
const MAX_BYTES = 10 * 1024 * 1024; // 10 MiB
const IMAGE_TYPES = ["image/jpeg", "image/jpg", "image/png", "image/webp"];

if (!IMAGE_TYPES.includes(file.type)) throw new Error("Please choose a JPG, PNG, or WebP image.");
if (file.size > MAX_BYTES)            throw new Error("File too large (max 10 MB).");
```

Handle RPC errors with the shared helper:

```ts
import { ConnectError } from "@connectrpc/connect";
import { errMessage } from "../lib/errors";

try {
  await uploadDocument({ teamId, resourceType, file });
} catch (err) {
  // toaster.create({ title: "Upload failed", description: errMessage(err), type: "error" });
}
```

| gRPC code | When | Fix |
|-----------|------|-----|
| `InvalidArgument` | disallowed `contentType`, `sizeBytes` `0`/over 10 MiB, missing `resourceType` | validate the file first |
| `Unauthenticated` | missing / invalid / expired token | ensure the user is signed in (token is auto-attached) |
| `PermissionDenied` | user has no role in `teamId` | use a team the user belongs to (`currentTeam.teamId`) |

---

## 6. Gotchas & troubleshooting

**`SignatureDoesNotMatch` on the PUT** — almost always the request differs from what was signed:
- The `Content-Type` you send on the PUT must **exactly** equal the `contentType` you passed to
  `RequestUpload` — same case, **no `; charset=…`** suffix. Just echo the response's `headers` map.
- Send the **raw `File`/`Blob`** as the body. Do **not** use `FormData` / `multipart/form-data`.
- **Never** run `uploadUrl` through `new URL()`, a query-string serializer, or any re-encoding —
  pass it to `fetch` verbatim.
- The URL **expires (~15 min)** — request and PUT in the same flow; don't stash it for later.

**Upload blocked by CORS (browser)** — the PUT goes cross-origin to `storage.googleapis.com`, so the
bucket needs a CORS policy allowing your frontend origin, the `PUT` method, and the `Content-Type`
request header. Example bucket CORS (ops/infra task, not frontend code):

```json
[
  {
    "origin": ["https://your-frontend.example.com", "http://localhost:5173"],
    "method": ["PUT", "GET"],
    "responseHeader": ["Content-Type"],
    "maxAgeSeconds": 3600
  }
]
```

**`publicUrl` returns 403 / not found** — the public URL only resolves if the bucket permits the
public-read ACL set at confirm time: the bucket must use **fine-grained ACLs** (not Uniform
Bucket-Level Access) and must not have **public access prevention** enforced. This is an infra
prerequisite, not a frontend bug.

---

## 7. End-to-end example (avatar upload)

```ts
import { documentClient } from "../lib/clients";
import { DocumentResourceType } from "../gen/document_iface/v1/document_pb";
import { ConnectError } from "@connectrpc/connect";
import { errMessage } from "../lib/errors";
import { uploadDocument } from "../lib/document"; // the helpers from section 3

async function onAvatarSelected(file: File, teamId: bigint) {
  try {
    const { documentId, publicUrl } = await uploadDocument({
      teamId,
      resourceType: DocumentResourceType.PROFILE_PICTURE,
      file,
    });
    // persist documentId on the profile; show the image immediately:
    return { documentId, publicUrl };
  } catch (err) {
    throw new Error(errMessage(err as ConnectError));
  }
}
```

```tsx
// minimal wiring (any UI lib):
<input
  type="file"
  accept="image/png,image/jpeg,image/webp"
  onChange={async (e) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const { publicUrl } = await onAvatarSelected(file, currentTeam!.teamId);
    setAvatarUrl(publicUrl);
  }}
/>
```

Private-document (e.g. invoice) view-on-demand:

```ts
async function openInvoice(documentId: string) {
  const { url } = await documentClient.getDownloadUrl({ documentId });
  window.open(url, "_blank"); // signed url, valid ~15 min
}
```

---

## 8. Reference

- Proto contract: [`schema/document_iface/v1/document.proto`](../../schema/document_iface/v1/document.proto)
- Generated TS client: `warehouse_frontend/src/gen/document_iface/v1/`
- Backend handlers: [`request_upload.go`](../document/request_upload.go),
  [`confirm_upload.go`](../document/confirm_upload.go),
  [`get_download_url.go`](../document/get_download_url.go)
- Validation & resource-type rules: [`service.go`](../document/service.go)
