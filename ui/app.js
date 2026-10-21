// cupo · panel. El estado llega de Go por cupo.onState (empujado por evento); los pedidos salen por window.act y
// window.win. La página no consulta nada: sólo un reloj de 20 s rehace los "en 1 h 42 min" mientras se ve.
(() => {
  'use strict';

  const $ = (sel) => document.querySelector(sel);
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const llamar = (fn, ...args) => {
    try {
      const r = fn?.(...args);
      r?.catch?.(() => {});
      return r;
    } catch (_) {
      return null;
    }
  };
  const act = (accion) => llamar(window.act, accion);
  const win = (cmd) => llamar(window.win, cmd);

  let S = null; // último estado recibido de Go
  let tab = 'todas';
  try { tab = localStorage.getItem('cupo.tab') || 'todas'; } catch (_) { /* sin almacenamiento: arranca en todas */ }

  // ---- formatos -----------------------------------------------------------------------------

  const nivel = (p) => (p < 60 ? 0 : p < 85 ? 1 : p < 100 ? 2 : 3); // los mismos cortes que el icono
  const pctTxt = (p) => (p > 0 && p < 1 ? '<1' : String(Math.round(p)));
  const hm = (ms) => new Date(ms).toLocaleTimeString('es-AR', { hourCycle: 'h23', hour: '2-digit', minute: '2-digit' });
  const diaCorto = (d) => d.toLocaleDateString('es-AR', { weekday: 'short', day: 'numeric' }).replace('.', '').replace(',', '');
  // 950 · 2,3 k · 268 k · 1,4 M (como cantidad() en Go)
  const cant = (n) => {
    for (const [div, suf] of [[1e9, ' G'], [1e6, ' M'], [1e3, ' k']]) {
      if (n >= div) {
        const v = n / div;
        return (v < 10 ? v.toFixed(1).replace('.', ',') : Math.round(v)) + suf;
      }
    }
    return String(Math.round(n));
  };

  // "16:00" · "mañana 07:00" · "dom 26 07:00" · "18 nov"
  function cuando(ms) {
    const f = new Date(ms);
    const dias = Math.round((new Date(ms).setHours(0, 0, 0, 0) - new Date().setHours(0, 0, 0, 0)) / 864e5);
    if (dias === 0) return hm(ms);
    if (dias === 1) return 'mañana ' + hm(ms);
    if (dias > 1 && dias < 7) return diaCorto(f) + ' ' + hm(ms);
    return f.toLocaleDateString('es-AR', { day: 'numeric', month: 'short' }).replace('.', '');
  }
  // "en 1 h 42 min" · "en 5 d 19 h" · "en 12 min"
  function falta(ms) {
    const s = Math.max(0, Math.round((ms - Date.now()) / 1000));
    const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
    if (d) return `en ${d} d` + (h ? ` ${h} h` : '');
    if (h) return `en ${h} h` + (m ? ` ${m} min` : '');
    return m ? `en ${m} min` : 'en menos de 1 min';
  }
  // "hace 12 s" · "hace 3 min" · "hace 2 h" · "hace 3 d"
  function hace(ms) {
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return `hace ${s} s`;
    if (s < 3600) return `hace ${Math.floor(s / 60)} min`;
    if (s < 86400) return `hace ${Math.floor(s / 3600)} h`;
    return `hace ${Math.floor(s / 86400)} d`;
  }
  const completo = (ms) => new Date(ms).toLocaleString('es-AR', { weekday: 'long', day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });

  // ---- lectura de una IA --------------------------------------------------------------------

  // vigente: si la ventana ya se renovó, el uso es 0 hasta el próximo pedido.
  const vigente = (f) => (f.renueva && f.renueva <= Date.now() ? 0 : f.pct);
  const ventanas = (x) => (x.filas || []).filter((f) => f.pct != null && !f.apagado && !f.cuota);
  const nombreDe = (f) => (f.grupo ? f.grupo + ' · ' : '') + f.nombre;

  // principal: lo que resume una IA en una fila. La ventana más usada; si no hay, el primer valor destacado.
  function principal(x) {
    const ms = ventanas(x);
    if (ms.length) return { medidor: ms.reduce((a, b) => (vigente(b) > vigente(a) ? b : a)) };
    const fs = x.filas || [];
    const v = fs.find((f) => f.card) || fs.find((f) => f.pct != null) || fs.find((f) => f.valor && !f.serie);
    return v ? (v.pct != null ? { medidor: v } : { valor: v }) : {};
  }

  // nivelIA: el color del punto de su pestaña.
  function nivelIA(x) {
    if (x.alerta) return 3;
    const p = principal(x);
    if (p.medidor) return nivel(vigente(p.medidor));
    if (x.error && !x.cuando) return 'e';
    return 'x';
  }

  function renovacion(f) {
    if (!f.renueva) return '';
    if (f.renueva <= Date.now()) return 'se renovó a las ' + hm(f.renueva);
    return falta(f.renueva) + ' · ' + cuando(f.renueva);
  }

  // ---- render -------------------------------------------------------------------------------

  let htmlTabs = '';
  let htmlPage = '';
  let tabMostrada = '';

  function render() {
    if (!S) return;
    const ias = S.ias || [];
    if (tab !== 'todas' && !ias.some((x) => x.id === tab) && S.faltan === 0) tab = 'todas';
    const x = ias.find((i) => i.id === tab);

    // la lista de pestañas: cada IA con su punto y, si tiene ventanas de uso, el % de la más ajustada
    const tabs = `<button class="tab" role="tab" data-tab="todas" aria-selected="${tab === 'todas'}"><span></span><span class="nom">todas</span></button><div class="sep"></div>` +
      ias.map((i) => {
        const p = principal(i);
        const pc = p.medidor && !p.medidor.cuota ? vigente(p.medidor) : null;
        return `<button class="tab" role="tab" data-tab="${esc(i.id)}" aria-selected="${i.id === tab}" title="${esc(i.nombre)}">` +
          `<span class="dot${i.cargando ? ' carga' : ''}" data-n="${nivelIA(i)}"></span><span class="nom">${esc(i.nombre.toLowerCase())}</span>` +
          (pc != null ? `<span class="pc" data-n="${nivel(pc)}">${pctTxt(pc)}%</span>` : '<span></span>') + '</button>';
      }).join('');
    if (tabs !== htmlTabs) $('#tabs').innerHTML = htmlTabs = tabs;

    const page = x ? htmlIA(x) : htmlTodas(ias);
    if (page !== htmlPage) {
      const pg = $('#page');
      if (tab !== tabMostrada) { // las barras crecen sólo al cambiar de pestaña, no con cada dato nuevo
        pg.classList.add('entra');
        setTimeout(() => pg.classList.remove('entra'), 800);
        pg.scrollTop = 0;
      }
      pg.innerHTML = htmlPage = page;
    }
    if (tab !== tabMostrada) {
      tabMostrada = tab;
      $(`.tab[data-tab="${CSS.escape(tab)}"]`)?.scrollIntoView({ block: 'nearest' });
    }

    // el punto del título: la primera ventana de la primera IA que tenga (el anillo de afuera del icono)
    const primera = ias.find((i) => ventanas(i).length);
    $('#mark').dataset.n = primera ? nivel(vigente(ventanas(primera)[0])) : '';
    $('#estado').textContent = S.faltan > 0 ? `actualizando · ${S.faltan === 1 ? 'falta 1' : 'faltan ' + S.faltan}`
      : S.ultima ? 'actualizado ' + hace(S.ultima) : '';
  }

  function htmlTodas(ias) {
    if (!ias.length) return '<div class="vacio"><b>buscando tus IAs…</b>la primera vuelta tarda unos segundos</div>';
    return '<div class="resumen">' + ias.map(filaResumen).join('') + '</div>';
  }

  function filaResumen(x) {
    const p = principal(x);
    let v, barra = '', sub, mal = x.alerta;
    if (p.medidor) {
      const f = p.medidor, pc = vigente(f), n = nivel(pc);
      v = `<span class="v" data-n="${n}">${pctTxt(pc)}<small>%</small></span>`;
      barra = `<div class="barra"><span data-n="${n}" style="width:${pc}%"></span></div>`;
      sub = [nombreDe(f), pc >= 100 ? 'agotado' : '', renovacion(f)].filter(Boolean).join(' · ');
    } else if (p.valor) {
      v = `<span class="v${x.alerta ? ' mal' : ''}">${esc(p.valor.valor)}</span>`;
      sub = [p.valor.nombre, p.valor.nota].filter(Boolean).join(' · ');
    } else {
      v = `<span class="v nada">${x.cargando ? '…' : '–'}</span>`;
      sub = x.cargando ? 'pidiendo…' : '';
    }
    if (x.alerta && x.nota) sub = x.nota;
    else if (!sub && x.nota) sub = x.nota;
    if (x.error) {
      sub = x.error + (x.cuando ? ' · dato de ' + hace(x.cuando) : '');
      mal = true;
    }
    return `<div class="ri" data-tab="${esc(x.id)}" tabindex="0" role="button" title="Ver ${esc(x.nombre)}">` +
      `<div class="ri-n"><span class="n">${esc(x.nombre)}</span>${x.plan ? `<span class="plan">${esc(x.plan)}</span>` : ''}` +
      `${x.estado ? `<span class="chip">${esc(x.estado)}</span>` : ''}</div>` +
      `${v}${barra}<div class="sub${mal ? ' mal' : ''}">${esc(sub)}</div></div>`;
  }

  // Una pestaña: cabecera, cuenta, avisos y después las filas en el orden en que llegan, en bloques: tarjetas
  // (medidores y valores destacados), gráficos por día y datos. Cada sección (grupo) lleva su título.
  function htmlIA(x) {
    let h = `<div class="cab"><h1>${esc(x.nombre)}</h1>${x.plan ? `<span class="plan">${esc(x.plan)}</span>` : ''}` +
      `${x.estado ? `<span class="chip">${esc(x.estado)}</span>` : ''}` +
      `<span class="edad">${x.cargando ? 'actualizando…' : x.cuando ? hace(x.cuando) : ''}</span></div>`;
    if (x.cuenta) h += `<div class="cuenta">${esc(x.cuenta)}</div>`;
    if (x.error) h += `<div class="error">${esc(x.error)}${x.cuando ? ' · muestro lo de ' + hace(x.cuando) : ''}</div>`;
    if (x.nota) h += `<div class="nota${x.alerta ? ' mal' : ''}">${esc(x.nota)}</div>`;

    const bloques = [];
    for (const f of x.filas || []) {
      const tipo = f.serie ? 'serie' : f.pct != null || f.card ? 'cards' : 'datos';
      const ult = bloques[bloques.length - 1];
      if (ult && ult.grupo === (f.grupo || '') && ult.tipo === tipo) ult.filas.push(f);
      else bloques.push({ grupo: f.grupo || '', tipo, filas: [f] });
    }
    let grupo = '';
    let primerCard = true;
    for (const b of bloques) {
      if (b.grupo !== grupo) {
        grupo = b.grupo;
        if (grupo) h += `<h2 class="sec">${esc(grupo)}</h2>`;
      }
      if (b.tipo === 'cards') {
        h += `<div class="cards">${b.filas.map((f) => {
          const c = card(f, x, primerCard);
          if (f.card) primerCard = false;
          return c;
        }).join('')}</div>`;
      } else if (b.tipo === 'serie') {
        h += b.filas.map(serie).join('');
      } else {
        h += `<div class="datos">${b.filas.map(datoHTML).join('')}</div>`;
      }
    }
    if (!(x.filas || []).length && !x.nota && !x.error) {
      h += `<div class="vacio"><b>${x.cargando ? 'pidiendo…' : 'sin datos todavía'}</b></div>`;
    }
    return h;
  }

  function card(f, x, primerCard) {
    if (f.pct == null) { // un valor destacado (saldo, tokens del mes)
      const mal = x.alerta && primerCard;
      return `<div class="card saldo${mal ? ' mal' : ''}"><span class="l">${esc(f.nombre)}</span>` +
        `<span class="num">${esc(f.valor)}</span>${f.nota ? `<span class="r">${esc(f.nota)}</span>` : ''}</div>`;
    }
    const pc = vigente(f), n = f.apagado ? 'x' : nivel(pc);
    const lineas = [];
    if (f.apagado) lineas.push(esc(f.nota || 'no aplica ahora'));
    else if (pc >= 100) lineas.push('<b>agotado</b>');
    if (f.valor) lineas.push(esc(f.valor));
    if (f.renueva && !f.apagado) lineas.push(esc(renovacion(f)));
    if (f.nota && !f.apagado) lineas.push(`<i>${esc(f.nota)}</i>`);
    const titulo = f.renueva ? ` title="se renueva el ${esc(completo(f.renueva))}"` : '';
    return `<div class="card${f.apagado ? ' apagado' : ''}" data-n="${n}"${titulo}><span class="l">${esc(f.nombre)}</span>` +
      `<span class="num">${pctTxt(pc)}<small>%</small></span>` +
      `<div class="barra"><span data-n="${n}" style="width:${pc}%"></span></div>` +
      (lineas.length ? `<span class="r">${lineas.join(' · ')}</span>` : '') + '</div>';
  }

  function datoHTML(f) {
    const nota = [f.nota, f.renueva ? 'vuelve ' + cuando(f.renueva) : ''].filter(Boolean).join(' · ');
    return `<div class="dato"><span class="k">${esc(f.nombre)}</span><span class="v">${esc(f.valor)}</span>` +
      (nota ? `<span class="n">${esc(nota)}</span>` : '') + '</div>';
  }

  // serie: una barra por día, la de hoy al final y más clara. El título de cada barra dice el día y el valor.
  function serie(f) {
    const vals = f.serie || [];
    const tope = Math.max(...vals, 1);
    const d0 = new Date(f.desde || Date.now());
    const barras = vals.map((v, i) => {
      const d = new Date(d0);
      d.setDate(d0.getDate() + i);
      const alto = v > 0 ? Math.max(4, (v / tope) * 100) : 0;
      return `<span class="${i === vals.length - 1 ? 'hoy' : ''}" style="height:${alto.toFixed(1)}%" title="${esc(diaCorto(d) + ' · ' + cant(v) + ' tokens')}"></span>`;
    }).join('');
    const primero = new Date(d0);
    return `<div class="serie"><div class="serie-cab"><span class="k">${esc(f.nombre)}</span>${f.valor ? `<span class="v">${esc(f.valor)}</span>` : ''}</div>` +
      `<div class="barras">${barras}</div>` +
      `<div class="ejes"><span>${esc(primero.toLocaleDateString('es-AR', { day: 'numeric', month: 'short' }).replace('.', ''))}</span>` +
      `<span>máx ${esc(cant(tope))}</span><span>hoy</span></div>` +
      (f.nota ? `<div class="n">${esc(f.nota)}</div>` : '') + '</div>';
  }

  setInterval(() => document.visibilityState === 'visible' && render(), 20_000);

  // ---- acciones -----------------------------------------------------------------------------

  function elegir(id) {
    tab = id;
    try { localStorage.setItem('cupo.tab', id); } catch (_) { /* no se recuerda: no pasa nada */ }
    render();
  }

  function confirmarSalir(btn) {
    if (btn.dataset.seguro) {
      btn.textContent = 'saliendo…';
      act('salir');
      return;
    }
    btn.dataset.seguro = '1';
    btn.textContent = '¿salir? tocá de nuevo';
    setTimeout(() => {
      delete btn.dataset.seguro;
      btn.textContent = 'salir';
    }, 3000);
  }

  document.addEventListener('click', (e) => {
    const n = e.target.closest('[data-win],[data-act],[data-tab]');
    if (!n) return;
    if (n.dataset.win) win(n.dataset.win);
    else if (n.dataset.act === 'salir') confirmarSalir(n);
    else if (n.dataset.act) act(n.dataset.act);
    else if (n.dataset.tab) elegir(n.dataset.tab);
  });

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') win('close');
    else if (e.key === 'F5' || ((e.ctrlKey || e.metaKey) && (e.key === 'r' || e.key === 'R'))) {
      e.preventDefault(); // WebView2 recargaría la página
      act('actualizar');
    } else if (S && (e.key === 'ArrowRight' || e.key === 'ArrowLeft' ||
      ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && e.target.closest?.('.tabs')))) { // ↑ ↓ sólo en la lista: si no, corren la página
      e.preventDefault();
      const ids = ['todas', ...(S.ias || []).map((x) => x.id)];
      const i = ids.indexOf(tab) + (e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : -1);
      elegir(ids[(i + ids.length) % ids.length]);
      $(`.tab[data-tab="${CSS.escape(tab)}"]`)?.focus();
    } else if (e.key === 'Enter' && e.target.dataset?.tab) elegir(e.target.dataset.tab);
  });

  document.querySelectorAll('[data-drag]').forEach((n) => {
    n.addEventListener('mousedown', (e) => {
      if (e.button === 0) win('drag');
    });
  });
  document.querySelectorAll('[data-rz]').forEach((n) => {
    n.addEventListener('mousedown', (e) => {
      if (e.button === 0) {
        e.preventDefault();
        win('resize:' + n.dataset.rz);
      }
    });
  });

  // ---- arranque -----------------------------------------------------------------------------
  window.cupo = {
    onState(s) {
      S = s;
      render();
    },
  };
  llamar(window.ready)?.then?.((s) => {
    if (s) window.cupo.onState(s);
  });
})();
