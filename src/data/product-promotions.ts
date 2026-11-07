import homeF30m from '../assets/client-1101/home-f30m.png';
import homeSt64ml from '../assets/client-1101/home-st64ml.png';
import homeSt64gl from '../assets/client-1101/home-st64gl.png';
import homeF30c from '../assets/client-1101/home-f30c.png';
import homeF30cl from '../assets/client-1101/home-f30cl.png';
import { products, categoryBySlug } from './catalog';

const bindings = [
  { id: 'p_f30m', group: 'long', image: homeF30m },
  { id: 'p_st64ml', group: 'long', image: homeSt64ml },
  { id: 'p_st64gl', group: 'long', image: homeSt64gl },
  { id: 'p_f30c', group: 'blower', image: homeF30c },
  { id: 'p_f30cl', group: 'blower', image: homeF30cl },
];
const promotions = bindings.flatMap(binding => {
  const product = products.find(product => product.id === binding.id);
  return product ? [{ ...binding, product, model: product.model, name: categoryBySlug(product.category).name }] : [];
});
export const longNosePromotions = promotions.filter(item => item.group === 'long');
export const blowerPromotions = promotions.filter(item => item.group === 'blower');
