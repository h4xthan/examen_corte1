import { Fragment, useCallback, useEffect, useState } from 'react'
import { api, apiPath, unsafeFetch } from '../api/client.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { money } from '../lib/books.jsx'

// The panel is a working tool, not a dashboard with six tiles.
//
// The college brief restructured the back office into three interfaces:
//
//   - Catálogo (Libros): the books table with its write operations and the
//     history of every change and consultation.
//   - Usuarios: alta, baja, modificación, consulta y asignación de permisos,
//     together with the history of who was moved and by whom.
//   - Respaldos: creating a dump of the whole database and downloading the ones
//     already taken.
//
// The management tabs that already existed — orders, coupons, reviews — stay,
// mounted for administrators alongside the three interfaces.
//
// The gate is not in this file. Nothing here decides anything about identity:
// every route below re-reads the role from the users table on every request, so
// hiding a tab is a courtesy and the server is the enforcement. A panel member
// with a stale tab open gets 403 and sees the message.

const ADMIN_TABS = [
  { key: 'books', label: 'Catálogo' },
  { key: 'users', label: 'Usuarios' },
  { key: 'backups', label: 'Respaldos' },
  { key: 'orders', label: 'Órdenes' },
  { key: 'coupons', label: 'Cupones' },
  { key: 'reviews', label: 'Reseñas' },
]

const STAFF_TABS = [{ key: 'books', label: 'Catálogo' }]

const ROLES = [
  { value: 'admin', label: 'admin' },
  { value: 'capturista', label: 'capturista' },
  { value: 'auditor', label: 'auditor' },
  { value: 'customer', label: 'customer' },
]

const ACTION_LABELS = {
  create: 'Alta',
  update: 'Modificación',
  delete: 'Baja',
  read: 'Consulta',
  role: 'Permisos',
  active: 'Estado',
}

// The same closed set the column's CHECK constraint and the service enforce.
// Listing it in the dropdown is a usability measure, not a control: a value that
// is not in this list is refused by the server with a 400.
const ORDER_STATUSES = ['pending', 'completed', 'shipped', 'delivered', 'cancelled', 'refunded']

const STATUS_LABELS = {
  pending: 'Pendiente',
  completed: 'Completada',
  shipped: 'Enviada',
  delivered: 'Entregada',
  cancelled: 'Cancelada',
  refunded: 'Reembolsada',
}

const EMPTY_BOOK = { author: '', title: '', pages: '', isbn: '', price_cents: '', stock: '', url_cover_image: '' }

export default function Admin() {
  const { user, isAdmin } = useAuth()
  const [tab, setTab] = useState('books')
  const isAuditor = user?.role === 'auditor'

  // The auditor sees the whole shop — every tab, all read-only — and the admin
  // sees the same tabs minus the catalogue capture, which is the capturista's.
  // The capturista works from the catalogue alone.
  const tabs = isAdmin || isAuditor ? ADMIN_TABS : STAFF_TABS
  const active = tabs.some((t) => t.key === tab) ? tab : 'books'

  return (
    <div>
      <h1>Panel de administración</h1>
      <p className="muted">
        Sesión de <strong>{user?.first_name} {user?.last_name}</strong> · rol{' '}
        <code>{user?.role}</code>.
      </p>
      <div className="tabs-section">
        <div className="tabs-bar">
          {tabs.map((t) => (
            <button key={t.key} className={`tab ${active === t.key ? 'on' : ''}`} onClick={() => setTab(t.key)}>
              {t.label}
            </button>
          ))}
        </div>
        <div className="tab-body">
          <Backoffice active={active} />
        </div>
      </div>
    </div>
  )
}

// Backoffice owns the data each tab displays and the reload-after-change
// discipline, so what the moderator sees is what the database holds. A panel
// that updates its own state optimistically is a panel that can lie about what
// was saved.
function Backoffice({ active }) {
  const { isAdmin, isCatalogWriter, user } = useAuth()
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const isAuditor = user?.role === 'auditor'

  const say = useCallback((kind, text) => {
    if (kind === 'error') {
      setError(text)
      setNotice('')
    } else {
      setNotice(text)
      setError('')
    }
  }, [])

  // run posts a change and then reloads the whole view load.
  const run = useCallback(async (method, path, body, successMessage) => {
    const res = await api[method](path, body)
    if (!res.ok) {
      say('error', res.data?.error || `${method} ${path}: error ${res.status}`)
      return false
    }
    say('ok', successMessage)
    return true
  }, [say])

  // Each tab renders read-only for the auditor and writable for the admin; the
  // catalogue is writable only by the capturista. The server is the control —
  // these flags only decide which controls to draw.
  return (
    <>
      {error && <p className="error">{error}</p>}
      {notice && <p className="ok">{notice}</p>}

      {active === 'books' && (
        <BooksTab reloadKey={notice} run={run} say={say} canWrite={isCatalogWriter} />
      )}
      {(active === 'users' && (isAdmin || isAuditor)) && (
        <UsersTab run={run} canWrite={isAdmin} />
      )}
      {(active === 'backups' && (isAdmin || isAuditor)) && (
        <BackupsTab say={say} canWrite={isAdmin} />
      )}
      {(active === 'orders' && (isAdmin || isAuditor)) && (
        <OrdersTab run={run} canWrite={isAdmin} />
      )}
      {(active === 'coupons' && (isAdmin || isAuditor)) && (
        <CouponsTab run={run} canWrite={isAdmin} />
      )}
      {(active === 'reviews' && (isAdmin || isAuditor)) && (
        <ReviewsTab run={run} canWrite={isAdmin} />
      )}
    </>
  )
}

// History is the one common piece of the three interfaces: the operating
// history the server keeps for the catalogue, the users directory and the
// backups. Being able to see it is exactly what makes an auditor useful.
function History({ entity, limit }) {
  const [rows, setRows] = useState([])
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setError('')
    api.get(`/admin/audit?entity=${entity}&limit=${limit || 200}`)
      .then((res) => {
        if (cancelled) return
        if (res.ok) {
          setRows(Array.isArray(res.data) ? res.data : [])
        } else {
          setError(res.data?.error || `Error ${res.status}`)
        }
      })
    return () => { cancelled = true }
  }, [entity, limit])

  if (error) return <p className="error">{error}</p>

  return (
    <>
      <h3>Historial</h3>
      <table className="table">
        <thead>
          <tr><th>Cuándo</th><th>Quién</th><th>Operación</th><th>Detalle</th></tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td>{new Date(r.created_at).toLocaleString()}</td>
              <td>{r.user_email || '—'}</td>
              <td>{ACTION_LABELS[r.action] || r.action}</td>
              <td>{r.entity_id ? `#${r.entity_id} ` : ''}{r.details}</td>
            </tr>
          ))}
          {rows.length === 0 && <tr><td colSpan="4" className="muted">Sin operaciones registradas.</td></tr>}
        </tbody>
      </table>
    </>
  )
}

// ---------------------------------------------------------------------------
// Interface 1 — Catálogo
// ---------------------------------------------------------------------------

function BooksTab({ run, say, canWrite }) {
  const [books, setBooks] = useState([])

  const load = useCallback(async () => {
    const res = await api.get('/books')
    if (res.ok && Array.isArray(res.data)) setBooks(res.data)
  }, [])

  useEffect(() => {
    load()
  }, [load, run])

  return (
    <>
      <Books books={books} onSave={async (method, path, body, msg) => {
        const ok = await run(method, path, body, msg)
        if (ok) load()
        return ok
      }} onNotice={say} canWrite={canWrite} />
      <div className="card">
        <History entity="book" />
      </div>
    </>
  )
}

function Books({ books, onSave, onNotice, canWrite }) {
  const [editing, setEditing] = useState(null)
  const [form, setForm] = useState(EMPTY_BOOK)

  function startNew() {
    setForm(EMPTY_BOOK)
    setEditing('new')
  }

  function startEdit(book) {
    setForm({
      author: book.author,
      title: book.title,
      pages: String(book.pages ?? ''),
      isbn: book.isbn ?? '',
      price_cents: String(book.price_cents ?? ''),
      stock: String(book.stock ?? ''),
      url_cover_image: book.url_cover_image ?? '',
    })
    setEditing(book.id)
  }

  async function submit(event) {
    event.preventDefault()
    // The form speaks cents to the server. It takes dollars from the moderator
    // and converts once, here, rather than sending 19.99 as a float and hoping
    // the arithmetic downstream survives it.
    const dollars = Number(form.price_cents)
    if (!Number.isFinite(dollars) || dollars < 0) {
      onNotice('error', 'El precio no es un número válido.')
      return
    }
    const body = {
      author: form.author,
      title: form.title,
      pages: Number(form.pages) || 0,
      isbn: form.isbn,
      price_cents: Math.round(dollars * 100),
      stock: Number(form.stock) || 0,
      url_cover_image: form.url_cover_image,
    }
    const ok = editing === 'new'
      ? await onSave('post', '/books', body, 'Libro creado.')
      : await onSave('put', `/books/${editing}`, body, 'Libro actualizado.')
    if (ok) setEditing(null)
  }

  async function remove(book) {
    if (!window.confirm(`¿Borrar «${book.title}»? Es permanente y se lleva sus reseñas.`)) return
    await onSave('del', `/books/${book.id}`, undefined, 'Libro borrado.')
  }

  return (
    <>
      <div className="card-head">
        <h2>Catálogo ({books.length})</h2>
        {canWrite && !editing && (
          <button className="btn btn-line btn-small" onClick={startNew}>Añadir libro</button>
        )}
      </div>

      {canWrite && editing && (
        <form className="card form admin-form" onSubmit={submit}>
          <h3>{editing === 'new' ? 'Nuevo libro' : `Editando #${editing}`}</h3>
          <div className="field-row">
            <div className="field">
              <label htmlFor="book-title">Título</label>
              <input id="book-title" required value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="book-author">Autor</label>
              <input id="book-author" required value={form.author}
                onChange={(e) => setForm({ ...form, author: e.target.value })} />
            </div>
          </div>
          <div className="field-row">
            <div className="field">
              {/* Dollars on screen, cents on the wire. See submit(). */}
              <label htmlFor="book-price">Precio (USD)</label>
              <input id="book-price" required inputMode="decimal" value={form.price_cents}
                onChange={(e) => setForm({ ...form, price_cents: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="book-stock">Stock</label>
              <input id="book-stock" required type="number" min="0" value={form.stock}
                onChange={(e) => setForm({ ...form, stock: e.target.value })} />
            </div>
          </div>
          <div className="field-row">
            <div className="field">
              <label htmlFor="book-pages">Páginas</label>
              <input id="book-pages" type="number" min="0" value={form.pages}
                onChange={(e) => setForm({ ...form, pages: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="book-isbn">ISBN</label>
              <input id="book-isbn" value={form.isbn}
                onChange={(e) => setForm({ ...form, isbn: e.target.value })} />
            </div>
          </div>
          <div className="field">
            <label htmlFor="book-cover">URL de la portada</label>
            <input id="book-cover" type="url" value={form.url_cover_image}
              onChange={(e) => setForm({ ...form, url_cover_image: e.target.value })} />
          </div>
          <div className="form inline">
            <button className="btn btn-solid" type="submit">Guardar</button>
            <button className="linklike" type="button" onClick={() => setEditing(null)}>Cancelar</button>
          </div>
        </form>
      )}

      <table className="table">
        <thead>
          <tr><th>ID</th><th>Título</th><th>Autor</th><th>Precio</th><th>Stock</th>{canWrite && <th />}</tr>
        </thead>
        <tbody>
          {books.map((b) => (
            <tr key={b.id}>
              <td>{b.id}</td>
              <td>{b.title}</td>
              <td>{b.author}</td>
              <td>{money(b.price_cents)}</td>
              <td>{b.stock}</td>
              {canWrite && (
                <td className="admin-actions">
                  <button className="linklike" onClick={() => startEdit(b)}>Editar</button>
                  <button className="linklike" onClick={() => remove(b)}>Borrar</button>
                </td>
              )}
            </tr>
          ))}
          {books.length === 0 && <tr><td colSpan={canWrite ? 6 : 5} className="muted">No hay libros.</td></tr>}
        </tbody>
      </table>
      {!canWrite && (
        <p className="muted">
          El catálogo es de solo lectura para este rol: la captura (alta,
          modificación y baja) corresponde al capturista. Leer un libro queda
          registrado en el historial de más abajo.
        </p>
      )}
    </>
  )
}

// ---------------------------------------------------------------------------
// Interface 2 — Usuarios
// ---------------------------------------------------------------------------

function UsersTab({ run, canWrite }) {
  const [users, setUsers] = useState([])

  const load = useCallback(async () => {
    const res = await api.get('/admin/users')
    if (res.ok && Array.isArray(res.data)) setUsers(res.data)
  }, [])

  useEffect(() => {
    load()
  }, [load, run])

  return (
    <>
      <Users users={users} canWrite={canWrite} onSave={async (method, path, body, msg) => {
        const ok = await run(method, path, body, msg)
        if (ok) load()
        return ok
      }} />
      <div className="card">
        <History entity="user" />
      </div>
    </>
  )
}

function Users({ users, onSave, canWrite }) {
  const { user: me } = useAuth()
  const [form, setForm] = useState({ first_name: '', last_name: '', email: '', password: '', role: 'customer' })
  const [editing, setEditing] = useState(null)
  const [editForm, setEditForm] = useState({ first_name: '', last_name: '', email: '' })

  async function alta(event) {
    event.preventDefault()
    const ok = await onSave(
      'post', '/admin/users',
      { ...form, email: form.email.trim(), role: form.role },
      'Usuario dado de alta.',
    )
    if (ok) setForm({ first_name: '', last_name: '', email: '', password: '', role: 'customer' })
  }

  function startEdit(u) {
    setEditing(u.id)
    setEditForm({ first_name: u.first_name, last_name: u.last_name, email: u.email })
  }

  async function saveEdit(id) {
    const ok = await onSave('put', `/admin/users/${id}`, editForm, 'Perfil modificado.')
    if (ok) setEditing(null)
  }

  async function baja(u) {
    if (!window.confirm(`¿Dar de baja a ${u.email}? Perderá el acceso y se cerrarán sus sesiones.`)) return
    // Only an actor that is not the account being removed can do this; the
    // server guards the same rule and answers 409 to self-baja.
    await onSave('del', `/admin/users/${u.id}`, undefined, 'Usuario dado de baja.')
  }

  async function reactivar(u) {
    await onSave('post', `/admin/users/${u.id}/activate`, {}, 'Usuario reactivado.')
  }

  return (
    <>
      <h2>Usuarios ({users.length})</h2>
      <p className="muted">
        La consulta es esta lista en vivo. Los permisos se leen de la base de
        datos en cada petición administrativa, así que un cambio de rol, una baja
        o una reactivación surten efecto en la sesión que ya tiene el usuario.
      </p>

      {canWrite && (
        <form className="card form admin-form" onSubmit={alta}>
          <h3>Alta de usuario</h3>
          <div className="field-row">
            <div className="field">
              <label htmlFor="user-first">Nombre</label>
              <input id="user-first" required value={form.first_name}
                onChange={(e) => setForm({ ...form, first_name: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="user-last">Apellidos</label>
              <input id="user-last" required value={form.last_name}
                onChange={(e) => setForm({ ...form, last_name: e.target.value })} />
            </div>
          </div>
          <div className="field-row">
            <div className="field">
              <label htmlFor="user-email">Correo</label>
              <input id="user-email" required type="email" value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="user-password">Contraseña</label>
              <input id="user-password" required type="password" autoComplete="new-password" value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="user-role">Rol</label>
              <select id="user-role" value={form.role}
                onChange={(e) => setForm({ ...form, role: e.target.value })}>
                {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
              </select>
            </div>
          </div>
          <button className="btn btn-solid" type="submit">Dar de alta</button>
        </form>
      )}

      <table className="table">
        <thead>
          <tr><th>ID</th><th>Email</th><th>Nombre</th><th>Rol</th><th>Estado</th>{canWrite && <th />}</tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>{u.id}</td>
              <td>
                {editing === u.id ? (
                  <input value={editForm.email}
                    onChange={(e) => setEditForm({ ...editForm, email: e.target.value })} />
                ) : u.email}
              </td>
              <td>
                {editing === u.id ? (
                  <span className="field-row">
                    <input value={editForm.first_name} aria-label="Nombre"
                      onChange={(e) => setEditForm({ ...editForm, first_name: e.target.value })} />
                    <input value={editForm.last_name} aria-label="Apellidos"
                      onChange={(e) => setEditForm({ ...editForm, last_name: e.target.value })} />
                  </span>
                ) : `${u.first_name} ${u.last_name}`}
              </td>
              <td>
                {canWrite ? (
                  <select
                    value={u.role}
                    aria-label={`Rol de ${u.email}`}
                    onChange={(e) => onSave('put', `/admin/users/${u.id}/role`, { role: e.target.value }, 'Rol actualizado.')}
                  >
                    {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
                  </select>
                ) : (
                  <span className={`badge-status ${u.is_active ? 's-completed' : 's-cancelled'}`}>{u.role}</span>
                )}
              </td>
              <td>
                <span className={`badge-status ${u.is_active ? 's-completed' : 's-cancelled'}`}>
                  {u.is_active ? 'Activo' : 'De baja'}
                </span>
              </td>
              {canWrite && (
                <td className="admin-actions">
                  {editing === u.id ? (
                    <>
                      <button className="linklike" onClick={() => saveEdit(u.id)}>Guardar</button>
                      <button className="linklike" onClick={() => setEditing(null)}>Cancelar</button>
                    </>
                  ) : (
                    <>
                      <button className="linklike" onClick={() => startEdit(u)}>Modificar</button>
                      {u.is_active
                        ? (me?.id !== u.id && <button className="linklike" onClick={() => baja(u)}>Baja</button>)
                        : <button className="linklike" onClick={() => reactivar(u)}>Reactivar</button>}
                    </>
                  )}
                </td>
              )}
            </tr>
          ))}
          {users.length === 0 && <tr><td colSpan={canWrite ? 6 : 5} className="muted">No hay usuarios.</td></tr>}
        </tbody>
      </table>
    </>
  )
}

// ---------------------------------------------------------------------------
// Interface 3 — Respaldos
// ---------------------------------------------------------------------------

function BackupsTab({ say, canWrite }) {
  const [rows, setRows] = useState([])
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    const res = await api.get('/admin/backups')
    if (res.ok && Array.isArray(res.data)) {
      setRows(res.data)
      setError('')
    } else {
      setError(res.data?.error || `Error ${res.status}`)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  async function create() {
    // The route answers with the dump itself as a file, so this request is not
    // the JSON helper: the response is the blob that becomes the download. The
    // server also keeps a copy in the backup directory, which is what the list
    // is fed from. unsafeFetch attaches the CSRF token and retries once if the
    // server saw a stale pair.
    const res = await unsafeFetch('/admin/backup', { method: 'POST' })
    if (!res.ok) {
      const body = await res.json().catch(() => null)
      say('error', body?.error || `Error ${res.status}`)
      return
    }
    const blob = await res.blob()
    const disposition = res.headers.get('Content-Disposition') || ''
    const match = disposition.match(/filename="([^"]+)"/)
    const filename = match ? match[1] : 'dvbs-backup.sql'
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
    say('ok', 'Respaldo creado y guardado en el servidor.')
    load()
  }

  async function download(name) {
    const res = await fetch(apiPath(`/admin/backups/${encodeURIComponent(name)}`), {
      credentials: 'include',
    })
    if (!res.ok) return
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  return (
    <>
      <div className="card-head">
        <h2>Respaldos ({rows.length})</h2>
        {canWrite && <button className="btn btn-solid btn-small" onClick={create}>Crear respaldo</button>}
      </div>
      <p className="muted">
        TiDB Cloud Starter no expone un BACKUP, así que esto vuelca la base
        completa — contraseñas y tokens incluidos — y lo guarda en el servidor.
        Sólo un administrador puede crear o descargar uno; el auditor ve la
        lista, no el contenido.
      </p>
      <table className="table">
        <thead>
          <tr><th>Archivo</th><th>Tamaño</th><th>Creado</th>{canWrite && <th />}</tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.name}>
              <td><code>{r.name}</code></td>
              <td>{formatBytes(r.size)}</td>
              <td>{new Date(r.created).toLocaleString()}</td>
              {canWrite && (
                <td>
                  <button className="linklike" onClick={() => download(r.name)}>Descargar</button>
                </td>
              )}
            </tr>
          ))}
          {rows.length === 0 && <tr><td colSpan={canWrite ? 4 : 3} className="muted">No hay respaldos todavía.</td></tr>}
        </tbody>
      </table>
      <div className="card">
        <History entity="backup" />
      </div>
    </>
  )
}

function formatBytes(n) {
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

// ---------------------------------------------------------------------------
// Management tabs (admin only)
// ---------------------------------------------------------------------------

function OrdersTab({ run, canWrite }) {
  const [orders, setOrders] = useState([])
  const load = useCallback(async () => {
    const res = await api.get('/orders')
    if (res.ok && Array.isArray(res.data)) setOrders(res.data)
  }, [])
  useEffect(() => { load() }, [load, run])

  return <Orders orders={orders} canWrite={canWrite} onSave={async (m, p, b, msg) => {
    const ok = await run(m, p, b, msg)
    if (ok) load()
    return ok
  }} />
}

function Orders({ orders, onSave, canWrite }) {
  const [expanded, setExpanded] = useState(null)

  async function loadItems(orderID) {
    const res = await api.get(`/orders/${orderID}`)
    if (!res.ok) return null
    return res.data
  }

  async function toggle(order) {
    if (expanded === order.id) {
      setExpanded(null)
      return
    }
    const detail = await loadItems(order.id)
    setExpanded(detail ? { id: order.id, items: detail.items || [] } : null)
  }

  return (
    <>
      <h2>Órdenes ({orders.length})</h2>
      <p className="muted">
        Mover una orden es un acto administrativo. El cliente ya no puede
        escribir su propio estado, y el servidor sólo acepta los estados de la
        lista cerrada.
      </p>
      <table className="table">
        <thead>
          <tr><th>ID</th><th>Cliente</th><th>Fecha</th><th>Total</th><th>Estado</th><th /></tr>
        </thead>
        <tbody>
          {orders.map((o) => (
            <Fragment key={o.id}>
              <tr>
                <td>
                  <button className="linklike" onClick={() => toggle(o)}>
                    #{o.id}
                  </button>
                </td>
                <td>{o.user_id}</td>
                <td>{o.created_at ? new Date(o.created_at).toLocaleDateString() : '—'}</td>
                <td>{money(o.total_cents)}</td>
                <td>
                  {canWrite ? (
                    <select
                      className={`badge-status s-${o.status}`}
                      value={o.status}
                      aria-label={`Estado de la orden ${o.id}`}
                      onChange={(e) => onSave('put', `/orders/${o.id}`, { status: e.target.value }, 'Estado actualizado.')}
                    >
                      {ORDER_STATUSES.map((s) => (
                        <option key={s} value={s}>{STATUS_LABELS[s]}</option>
                      ))}
                    </select>
                  ) : (
                    <span className={`badge-status s-${o.status}`}>{STATUS_LABELS[o.status] || o.status}</span>
                  )}
                </td>
                <td>
                  <button className="linklike" onClick={() => toggle(o)}>
                    {expanded === o.id ? 'Ocultar' : 'Líneas'}
                  </button>
                </td>
              </tr>
              {expanded === o.id && (
                <tr>
                  <td colSpan="6">
                    {expanded.items.length === 0 ? (
                      <span className="muted">Sin líneas.</span>
                    ) : expanded.items.map((item) => (
                      <div key={item.id} className="order-meta">
                        <span>Libro #{item.book_id}</span>
                        <span>{item.quantity} × {money(item.unit_price_cents)}</span>
                      </div>
                    ))}
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
          {orders.length === 0 && <tr><td colSpan="6" className="muted">No hay órdenes.</td></tr>}
        </tbody>
      </table>
    </>
  )
}

function CouponsTab({ run, canWrite }) {
  const [coupons, setCoupons] = useState([])
  const load = useCallback(async () => {
    const res = await api.get('/coupons')
    if (res.ok && Array.isArray(res.data)) setCoupons(res.data)
  }, [])
  useEffect(() => { load() }, [load, run])

  return <Coupons coupons={coupons} canWrite={canWrite} onSave={async (m, p, b, msg) => {
    const ok = await run(m, p, b, msg)
    if (ok) load()
    return ok
  }} />
}

function Coupons({ coupons, onSave, canWrite }) {
  const [form, setForm] = useState({ code: '', discount_percent: '', max_uses: '' })

  async function create(event) {
    event.preventDefault()
    const percent = Number(form.discount_percent)
    const maxUses = Number(form.max_uses)
    if (!Number.isInteger(percent) || percent < 1 || percent > 100) return
    if (!Number.isInteger(maxUses) || maxUses < 1) return
    const ok = await onSave('post', '/coupons', {
      code: form.code,
      discount_percent: percent,
      max_uses: maxUses,
      expires_at: new Date(Date.now() + 30 * 24 * 3600 * 1000).toISOString(),
    }, 'Cupón creado.')
    if (ok) setForm({ code: '', discount_percent: '', max_uses: '' })
  }

  async function remove(coupon) {
    if (!window.confirm(`¿Retirar el cupón ${coupon.code}?`)) return
    await onSave('del', `/coupons/${coupon.id}`, undefined, 'Cupón retirado.')
  }

  const expiryInvalid = Number(form.discount_percent) < 1 || Number(form.discount_percent) > 100 ||
    Number(form.max_uses) < 1

  return (
    <>
      <h2>Cupones ({coupons.length})</h2>

      {canWrite && (
        <form className="card form admin-form" onSubmit={create}>
          <h3>Nuevo cupón</h3>
          <div className="field-row">
            <div className="field">
              <label htmlFor="coupon-code">Código</label>
              <input id="coupon-code" required value={form.code}
                onChange={(e) => setForm({ ...form, code: e.target.value.toUpperCase() })} />
            </div>
            <div className="field">
              <label htmlFor="coupon-percent">Descuento (%)</label>
              <input id="coupon-percent" required type="number" min="1" max="100" value={form.discount_percent}
              onChange={(e) => setForm({ ...form, discount_percent: e.target.value })} />
          </div>
          <div className="field">
            <label htmlFor="coupon-max">Usos máximos</label>
            <input id="coupon-max" required type="number" min="1" value={form.max_uses}
              onChange={(e) => setForm({ ...form, max_uses: e.target.value })} />
          </div>
        </div>
        <button className="btn btn-solid" type="submit" disabled={expiryInvalid}>Crear cupón</button>
        {expiryInvalid && <span className="muted">Entre 1 y 100 % de descuento, y al menos un uso.</span>}
      </form>
      )}

      <table className="table">
        <thead>
          <tr><th>Código</th><th>Descuento</th><th>Usos</th><th>Caduca</th>{canWrite && <th />}</tr>
        </thead>
        <tbody>
          {coupons.map((c) => (
            <tr key={c.id}>
              <td><code>{c.code}</code></td>
              <td>{c.discount_percent}%</td>
              <td>{c.used_count} / {c.max_uses}</td>
              <td>{c.expires_at ? new Date(c.expires_at).toLocaleDateString() : 'nunca'}</td>
              {canWrite && (
                <td>
                  <button className="linklike" onClick={() => remove(c)}>Retirar</button>
                </td>
              )}
            </tr>
          ))}
          {coupons.length === 0 && <tr><td colSpan={canWrite ? 5 : 4} className="muted">No hay cupones.</td></tr>}
        </tbody>
      </table>
    </>
  )
}

function ReviewsTab({ run, canWrite }) {
  const [reviews, setReviews] = useState([])
  const load = useCallback(async () => {
    const res = await api.get('/admin/reviews')
    if (res.ok && Array.isArray(res.data)) setReviews(res.data)
  }, [])
  useEffect(() => { load() }, [load, run])

  return <Reviews reviews={reviews} canWrite={canWrite} onSave={async (m, p, b, msg) => {
    const ok = await run(m, p, b, msg)
    if (ok) load()
    return ok
  }} />
}

function Reviews({ reviews, onSave, canWrite }) {
  async function remove(review) {
    if (!window.confirm(`¿Borrar la reseña de «${review.book_title}»? Es permanente.`)) return
    await onSave('del', `/reviews/${review.id}`, undefined, 'Reseña borrada.')
  }

  return (
    <>
      <h2>Reseñas ({reviews.length})</h2>
      <p className="muted">
        Sólo pueden existir sobre una compra completada: la base de datos exige
        que la línea pertenezca a una orden en estado <code>completed</code>.
      </p>
      <table className="table">
        <thead>
          <tr><th>Libro</th><th>Autor de la reseña</th><th>Nota</th><th>Comentario</th>{canWrite && <th />}</tr>
        </thead>
        <tbody>
          {reviews.map((r) => (
            <tr key={r.id}>
              <td>{r.book_title}</td>
              <td>{r.author_email}</td>
              <td>{r.rating} / 5</td>
              <td>{r.comment || '—'}</td>
              {canWrite && (
                <td>
                  <button className="linklike" onClick={() => remove(r)}>Borrar</button>
                </td>
              )}
            </tr>
          ))}
          {reviews.length === 0 && <tr><td colSpan={canWrite ? 5 : 4} className="muted">No hay reseñas.</td></tr>}
        </tbody>
      </table>
    </>
  )
}