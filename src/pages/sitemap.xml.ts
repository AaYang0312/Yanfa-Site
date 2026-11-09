import type { APIRoute } from 'astro';
import { products, legacySeries } from '../data/catalog';

export const GET: APIRoute = ({ site }) => {
  const paths = ['/', '/products/', '/about/', '/service/', '/media/', '/shop/', '/contact/', '/privacy/',
    ...products.map(product => `/products/${product.slug}/`),
    ...legacySeries.filter(series => series.items.length).map(series => `/products/${series.slug}/`)];
  const escapeXML = (text: string) => text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;');
  return new Response(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${paths.map(path => `<url><loc>${escapeXML(new URL(path, site).href)}</loc></url>`).join('')}</urlset>`, {
    headers: { 'Content-Type': 'application/xml; charset=utf-8' },
  });
};
