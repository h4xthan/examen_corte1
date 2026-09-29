import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api/client.js'
import { Cover, money } from '../lib/books.jsx'

const ORDENES = {
  destacados: { label: 'Destacados', fn: null },
  populares: { label: 'Populares', fn: (a, b) => b.pages - a.pages },
  nuevos: { label: 'Novedades', fn: (a, b) => b.id - a.id },
  titulo: { label: 'Título A–Z', fn: (a, b) => a.title.localeCompare(b.title) },
  precio_asc: { label: 'Precio: menor a mayor', fn: (a, b) => a.price_cents - b.price_cents },
  precio_desc: { label: 'Precio: mayor a menor', fn: (a, b) => b.price_cents - a.price_cents },
}

const RANGOS = [
  { id: 'todos', label: 'Todos los precios', test: () => true },
  { id: 'bajo', label: 'Menos de $8.00', test: (p) => p < 800 },
  { id: 'medio', label: '$8.00 – $12.00', test: (p) => p >= 800 && p <= 1200 },
  { id: 'alto', label: 'Más de $12.00', test: (p) => p > 1200 },
]

export default function Catalog() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const [books, setBooks] = useState([])
  const [q, setQ] = useState(params.get('q') || '')
  const [autor, setAutor] = useState('')
  const [soloStock, setSoloStock] = useState(false)
  const [rango, setRango] = useState('todos')
  const [orden, setOrden] = useState(params.get('orden') || 'destacados')

  useEffect(() => {
    api.get('/books').then((res) => setBooks(res.data || []))
  }, [])

  useEffect(() => {
    setQ(params.get('q') || '')
    setOrden(params.get('orden') || 'destacados')
  }, [params])

  const autores = useMemo(
    () => [...new Set(books.map((b) => b.author))].sort((a, b) => a.localeCompare(b)),
    [books],
  )

  const filtered = useMemo(() => {
    let list = books.filter((b) => {
      const needle = q.toLowerCase()
      if (needle && !`${b.title} ${b.author}`.toLowerCase().includes(needle)) return false
      if (autor && b.author !== autor) return false
      if (soloStock && b.stock === 0) return false
      const range = RANGOS.find((r) => r.id === rango)
      if (!range.test(b.price_cents)) return false
      return true
    })
    const sortFn = ORDENES[orden]?.fn
    if (sortFn) list = [...list].sort(sortFn)
    return list
  }, [books, q, autor, soloStock, rango, orden])

  return (
    <div className="catalog">
      <aside className="filters">
        <h2 className="filters-title">Ficha del catálogo</h2>

        <div className="filter-group">
          <label className="filter-label">Búsqueda</label>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Título o autor…" />
        </div>

        <div className="filter-group">
          <label className="filter-label">Autor</label>
          <select value={autor} onChange={(e) => setAutor(e.target.value)}>
            <option value="">Todos</option>
            {autores.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </select>
        </div>

        <div className="filter-group">
          <span className="filter-label">Precio</span>
          {RANGOS.map((r) => (
            <label key={r.id} className="radio-line">
              <input type="radio" name="rango" checked={rango === r.id} onChange={() => setRango(r.id)} />
              <span>{r.label}</span>
            </label>
          ))}
        </div>

        <div className="filter-group">
          <span className="filter-label">Disponibilidad</span>
          <label className="radio-line">
            <input type="checkbox" checked={soloStock} onChange={(e) => setSoloStock(e.target.checked)} />
            <span>Solo en stock</span>
          </label>
        </div>

        <button
          className="btn btn-ghost"
          onClick={() => {
            setQ('')
            setAutor('')
            setSoloStock(false)
            setOfertas(false)
            setRango('todos')
            navigate('/catalogo')
          }}
        >
          Limpiar filtros
        </button>
      </aside>

      <section className="catalog-results">
        <header className="section-head">
          <h2>{ORDENES[orden]?.label || 'Catálogo'}</h2>
          <span className="count-note">{filtered.length} títulos</span>
        </header>

        <div className="dense-grid">
          {filtered.map((book) => (
            <article key={book.id} className="dcard" onClick={() => navigate(`/libro/${book.id}`)}>
              <div className="dcard-cover">
                <Cover book={book} />
                {book.stock === 0 && <span className="badge badge-out">Agotado</span>}
              </div>
              <h3 className="dcard-title">{book.title}</h3>
              <p className="dcard-author">{book.author}</p>
              <p className="dcard-price"><b>{money(book.price_cents)}</b></p>
            </article>
          ))}
          {filtered.length === 0 && <p className="muted">Ningún título coincide con la ficha de filtros.</p>}
        </div>
      </section>
    </div>
  )
}